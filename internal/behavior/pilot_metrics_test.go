package behavior_test

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"
)

//go:embed testdata/pilot-metrics.json
var pilotMetricData []byte

type pilotMetricSet struct {
	Version                  int                     `json:"version"`
	DatasetKind              string                  `json:"dataset_kind"`
	RealPilotDataStatus      string                  `json:"real_pilot_data_status"`
	ImprovementTargetPercent json.RawMessage         `json:"improvement_target_percent"`
	Metrics                  []pilotMetricDefinition `json:"metrics"`
	SyntheticCases           []syntheticMetricCase   `json:"synthetic_cases"`
}

type pilotMetricDefinition struct {
	ID                  string          `json:"id"`
	Label               string          `json:"label"`
	Unit                string          `json:"unit"`
	Definition          string          `json:"definition"`
	CollectionStatus    string          `json:"collection_status"`
	CurrentSourceFields []string        `json:"current_source_fields"`
	RequiredPilotEvents []string        `json:"required_pilot_events"`
	PilotValue          json.RawMessage `json:"pilot_value"`
}

type syntheticMetricCase struct {
	ID        string                 `json:"id"`
	Synthetic bool                   `json:"synthetic"`
	Events    []syntheticMetricEvent `json:"events"`
	Expected  syntheticMetricValues  `json:"expected"`
}

type syntheticMetricEvent struct {
	Type     string `json:"type"`
	OffsetMS int64  `json:"offset_ms"`
}

type syntheticMetricValues struct {
	RequestToResultDurationMS int64 `json:"request_to_result_duration_ms"`
	ManualHandoffCount        int   `json:"manual_handoff_count"`
	ApprovalFollowupCount     int   `json:"approval_followup_count"`
	OperationReviewDurationMS int64 `json:"operation_review_duration_ms"`
}

func TestPilotMetricDataKeepsRealValuesUnknownAndHasNoImprovementTarget(t *testing.T) {
	data := loadPilotMetrics(t)
	if data.Version != 1 || data.DatasetKind != "SYNTHETIC_TEST_SPECIFICATION" ||
		data.RealPilotDataStatus != "AWAITING_PILOT" ||
		!bytes.Equal(bytes.TrimSpace(data.ImprovementTargetPercent), []byte("null")) {
		t.Fatal("pilot metric data must remain a synthetic specification without a real baseline or improvement target")
	}

	type expectedDefinition struct {
		label    string
		unit     string
		status   string
		sources  []string
		required []string
	}
	expected := map[string]expectedDefinition{
		"request_to_result_duration": {
			label: "申请到结果用时", unit: "milliseconds", status: "DERIVABLE_NOW",
			sources: []string{"authorization_requests.created_at", "executions.completed_at"},
		},
		"manual_handoff_count": {
			label: "人工转交次数", unit: "count", status: "AWAITING_PILOT_CAPTURE",
			required: []string{"manual_handoff"},
		},
		"approval_followup_count": {
			label: "审批补问次数", unit: "count", status: "AWAITING_PILOT_CAPTURE",
			required: []string{"approval_followup"},
		},
		"operation_review_duration": {
			label: "复盘一次操作所需时间", unit: "milliseconds", status: "AWAITING_PILOT_CAPTURE",
			required: []string{"review_started", "review_completed"},
		},
	}
	if len(data.Metrics) != len(expected) {
		t.Fatalf("pilot metric count=%d want=%d", len(data.Metrics), len(expected))
	}
	seen := make(map[string]bool, len(data.Metrics))
	for _, metric := range data.Metrics {
		want, ok := expected[metric.ID]
		if !ok || seen[metric.ID] {
			t.Fatalf("pilot metric has an unexpected or duplicate id %q", metric.ID)
		}
		seen[metric.ID] = true
		if metric.Label != want.label || metric.Unit != want.unit || metric.CollectionStatus != want.status ||
			strings.TrimSpace(metric.Definition) == "" || !slices.Equal(metric.CurrentSourceFields, want.sources) ||
			!slices.Equal(metric.RequiredPilotEvents, want.required) {
			t.Fatalf("pilot metric %q does not match its collection contract", metric.ID)
		}
		if !bytes.Equal(bytes.TrimSpace(metric.PilotValue), []byte("null")) {
			t.Fatalf("pilot metric %q must not turn an uncollected real value into zero or a synthetic baseline", metric.ID)
		}
	}

	for _, forbiddenKey := range [][]byte{
		[]byte(`"password"`), []byte(`"token"`), []byte(`"cookie"`),
		[]byte(`"secret"`), []byte(`"credential"`), []byte(`"vault_path"`),
	} {
		if bytes.Contains(pilotMetricData, forbiddenKey) {
			t.Fatal("pilot metric data contains a forbidden secret-bearing field")
		}
	}
}

func TestSyntheticPilotMetricCaseCalculatesAllFourMeasures(t *testing.T) {
	data := loadPilotMetrics(t)
	if len(data.SyntheticCases) != 1 || !data.SyntheticCases[0].Synthetic {
		t.Fatal("pilot metric calculation needs exactly one explicitly synthetic case")
	}
	fixture := data.SyntheticCases[0]
	if fixture.ID == "" {
		t.Fatal("synthetic pilot metric case has no id")
	}
	if fixture.Expected.RequestToResultDurationMS <= 0 || fixture.Expected.ManualHandoffCount <= 0 ||
		fixture.Expected.ApprovalFollowupCount <= 0 || fixture.Expected.OperationReviewDurationMS <= 0 {
		t.Fatal("synthetic pilot metric case must exercise all four calculations")
	}
	actual := calculateSyntheticMetrics(t, fixture.Events)
	if actual != fixture.Expected {
		t.Fatalf("synthetic pilot metrics=%+v want=%+v", actual, fixture.Expected)
	}
}

func loadPilotMetrics(t *testing.T) pilotMetricSet {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(pilotMetricData))
	decoder.DisallowUnknownFields()
	var result pilotMetricSet
	if err := decoder.Decode(&result); err != nil {
		t.Fatal("decode pilot metric test data")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatal("pilot metric test data has trailing content")
	}
	return result
}

func calculateSyntheticMetrics(t *testing.T, events []syntheticMetricEvent) syntheticMetricValues {
	t.Helper()
	var requestedAt, resultAt, reviewStartedAt, reviewCompletedAt int64
	var hasRequest, hasResult, hasReviewStart, hasReviewCompletion bool
	var result syntheticMetricValues
	var previous int64
	for index, event := range events {
		if event.OffsetMS < 0 || (index > 0 && event.OffsetMS < previous) {
			t.Fatal("synthetic pilot events must use non-negative, ordered offsets")
		}
		previous = event.OffsetMS
		switch event.Type {
		case "authorization_requested":
			if hasRequest {
				t.Fatal("synthetic pilot case contains duplicate authorization request events")
			}
			requestedAt, hasRequest = event.OffsetMS, true
		case "result_completed":
			if hasResult {
				t.Fatal("synthetic pilot case contains duplicate result completion events")
			}
			resultAt, hasResult = event.OffsetMS, true
		case "manual_handoff":
			result.ManualHandoffCount++
		case "approval_followup":
			result.ApprovalFollowupCount++
		case "review_started":
			if hasReviewStart {
				t.Fatal("synthetic pilot case contains duplicate review start events")
			}
			reviewStartedAt, hasReviewStart = event.OffsetMS, true
		case "review_completed":
			if hasReviewCompletion {
				t.Fatal("synthetic pilot case contains duplicate review completion events")
			}
			reviewCompletedAt, hasReviewCompletion = event.OffsetMS, true
		default:
			t.Fatalf("synthetic pilot case contains unknown event %q", event.Type)
		}
	}
	if !hasRequest || !hasResult || !hasReviewStart || !hasReviewCompletion ||
		resultAt < requestedAt || reviewCompletedAt < reviewStartedAt {
		t.Fatal("synthetic pilot case has an incomplete or reversed duration boundary")
	}
	result.RequestToResultDurationMS = resultAt - requestedAt
	result.OperationReviewDurationMS = reviewCompletedAt - reviewStartedAt
	return result
}
