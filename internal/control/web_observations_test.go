package control

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/identity"
	"github.com/fallingnight/akv/internal/observation"
)

const (
	webObservationRequestID = "00000000-0000-4000-8000-000000000011"
	webObservationReviewID  = "00000000-0000-4000-8000-000000000012"
	webObservationKey       = "00000000-0000-4000-8000-000000000013"
)

type webObservationCall struct {
	requestID, reviewID, key string
	eventType                observation.EventType
}

type webObservationFake struct {
	calls   []webObservationCall
	summary observation.Summary
}

func (fake *webObservationFake) Record(
	_ context.Context,
	_ identity.User,
	requestID string,
	eventType observation.EventType,
	reviewID string,
	key string,
) (observation.Event, bool, error) {
	fake.calls = append(fake.calls, webObservationCall{
		requestID: requestID, reviewID: reviewID, key: key, eventType: eventType,
	})
	return observation.Event{
		ID: webObservationKey, RequestID: requestID, Type: eventType,
		ReviewSessionID: reviewID, OccurredAt: time.Unix(1, 0).UTC(),
	}, true, nil
}

func (fake *webObservationFake) Summarize(
	_ context.Context,
	_ identity.User,
	requestID string,
) (observation.Summary, error) {
	result := fake.summary
	result.RequestID = requestID
	return result, nil
}

func TestWebObservationRoutesUseCSRFStrictBodiesAndIdempotency(t *testing.T) {
	fixtures := []struct {
		path      string
		eventType observation.EventType
		reviewID  string
	}{
		{"/v1/web/authorizations/" + webObservationRequestID + "/observations/manual-handoff", observation.EventManualHandoff, ""},
		{"/v1/web/authorizations/" + webObservationRequestID + "/observations/approval-followup", observation.EventApprovalFollowup, ""},
		{"/v1/web/authorizations/" + webObservationRequestID + "/reviews", observation.EventReviewStarted, ""},
		{"/v1/web/authorizations/" + webObservationRequestID + "/reviews/" + webObservationReviewID + "/complete", observation.EventReviewCompleted, webObservationReviewID},
	}
	for _, fixture := range fixtures {
		t.Run(string(fixture.eventType), func(t *testing.T) {
			fake := &webObservationFake{}
			server := testWebObservationServer(fake)
			request := authenticatedWebRequest(http.MethodPost, fixture.path, `{}`)
			request.Header.Set(observationIdempotencyHeader, webObservationKey)
			response := httptest.NewRecorder()
			server.Handler.ServeHTTP(response, request)
			if response.Code != http.StatusCreated || len(fake.calls) != 1 {
				t.Fatalf("status=%d calls=%d body=%q", response.Code, len(fake.calls), response.Body.String())
			}
			call := fake.calls[0]
			if call.requestID != webObservationRequestID || call.eventType != fixture.eventType ||
				call.reviewID != fixture.reviewID || call.key != webObservationKey {
				t.Fatalf("observation call=%+v", call)
			}
		})
	}

	fake := &webObservationFake{}
	server := testWebObservationServer(fake)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/web/authorizations/"+webObservationRequestID+"/observations/manual-handoff",
		strings.NewReader(`{}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session-secret"})
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || len(fake.calls) != 0 {
		t.Fatalf("missing CSRF status=%d calls=%d", response.Code, len(fake.calls))
	}

	request = authenticatedWebRequest(
		http.MethodPost,
		"/v1/web/authorizations/"+webObservationRequestID+"/observations/manual-handoff",
		`{"note":"must not be accepted"}`,
	)
	request.Header.Set(observationIdempotencyHeader, webObservationKey)
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || len(fake.calls) != 0 {
		t.Fatalf("unknown field status=%d calls=%d", response.Code, len(fake.calls))
	}
}

func TestWebObservationSummaryKeepsUnknownValuesNull(t *testing.T) {
	fake := &webObservationFake{summary: observation.Summary{
		CaptureStatus: observation.CaptureUnknown, ReviewStatus: observation.ReviewUnknown,
	}}
	server := testWebObservationServer(fake)
	request := authenticatedWebRequest(
		http.MethodGet, "/v1/web/authorizations/"+webObservationRequestID+"/pilot-metrics", "",
	)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `"capture_status":"UNKNOWN"`) ||
		!strings.Contains(body, `"manual_handoff_count":null`) ||
		!strings.Contains(body, `"improvement_target_percent":null`) {
		t.Fatalf("summary status=%d body=%q", response.Code, body)
	}
}

func testWebObservationServer(manager WebObservationManager) *http.Server {
	config := Config{ListenAddress: "127.0.0.1:0", PublicOrigin: "https://akv.example.test"}
	return NewServer(
		config,
		slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
		nil,
		&WebRuntime{Identity: &webIdentityFake{}, Observations: manager},
	)
}
