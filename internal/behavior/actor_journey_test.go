package behavior_test

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

//go:embed testdata/actor-journeys.json
var actorJourneyData []byte

type actorJourneySet struct {
	Version  int            `json:"version"`
	Journeys []actorJourney `json:"journeys"`
}

type actorJourney struct {
	ID               string       `json:"id"`
	Description      string       `json:"description"`
	Actors           []string     `json:"actors"`
	Checkpoints      []string     `json:"checkpoints"`
	ExpectedManifest dataManifest `json:"expected_manifest"`
}

func TestActorJourneyDataIsDeclarative(t *testing.T) {
	journeys := loadActorJourneys(t)
	if journeys.Version != 1 || len(journeys.Journeys) != 2 {
		t.Fatal("unexpected behavior journey data version or count")
	}
	for _, journey := range journeys.Journeys {
		if journey.ID == "" || journey.Description == "" || len(journey.Actors) == 0 || len(journey.Checkpoints) == 0 {
			t.Fatal("behavior journey data is incomplete")
		}
	}
	for _, forbiddenKey := range [][]byte{
		[]byte(`"password"`), []byte(`"token"`), []byte(`"cookie"`),
		[]byte(`"secret"`), []byte(`"credential"`), []byte(`"vault_path"`),
	} {
		if bytes.Contains(actorJourneyData, forbiddenKey) {
			t.Fatal("behavior journey data contains a forbidden secret-bearing field")
		}
	}
}

func TestOrdinaryUserAndAgentCompleteApprovedOperationOnce(t *testing.T) {
	journey := actorJourneyByID(t, "ordinary-user-approved-once")
	harness := newBehaviorHarness(t)
	owner := harness.registerWebActor(t, "behavior-owner")
	primaryAgent := owner.registerAgent(t, "primary-agent")
	unrelatedUser := harness.registerWebActor(t, "behavior-unrelated")
	unrelatedAgent := unrelatedUser.registerAgent(t, "unrelated-agent")

	status, targetsBody := harness.agentCall(t, harness.control.URL, http.MethodGet, "/v1/agent/targets", primaryAgent.Token, nil)
	if status != http.StatusOK {
		t.Fatalf("discover behavior targets status=%d", status)
	}
	assertNoRuntimeValues(t, targetsBody, primaryAgent.Token, harness.protectedValue, harness.vaultPath, harness.target.URL)
	var targets []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		OperationsURL string `json:"operations_url"`
	}
	decodeBehaviorJSON(t, targetsBody, &targets)
	if len(targets) != 1 || targets[0].ID != harness.targetRecord.ID || targets[0].OperationsURL == "" {
		t.Fatal("Agent did not discover the configured behavior target")
	}

	status, operationsBody := harness.agentCall(
		t, harness.control.URL, http.MethodGet, targets[0].OperationsURL, primaryAgent.Token, nil,
	)
	if status != http.StatusOK {
		t.Fatalf("discover behavior operations status=%d", status)
	}
	assertNoRuntimeValues(t, operationsBody, primaryAgent.Token, harness.protectedValue, harness.vaultPath, harness.target.URL)
	var operationCatalog struct {
		Operations []struct {
			OperationID string          `json:"operation_id"`
			Version     int             `json:"version"`
			Key         string          `json:"key"`
			Schema      json.RawMessage `json:"arguments_schema"`
		} `json:"operations"`
	}
	decodeBehaviorJSON(t, operationsBody, &operationCatalog)
	if len(operationCatalog.Operations) != 1 ||
		operationCatalog.Operations[0].OperationID != harness.operationRecord.ID ||
		operationCatalog.Operations[0].Version != harness.operationVersion.Version ||
		len(operationCatalog.Operations[0].Schema) == 0 {
		t.Fatal("Agent did not discover the exact public behavior operation")
	}

	primaryTaskID := beginBehaviorTask(t, harness, primaryAgent.Token)
	heartbeatBehaviorTask(t, harness, primaryAgent.Token, primaryTaskID)
	requestID := submitBehaviorAuthorization(
		t, harness, primaryAgent.Token, primaryTaskID,
		operationCatalog.Operations[0].OperationID, operationCatalog.Operations[0].Version,
	)

	status, _ = harness.agentCall(t, harness.execution.URL, http.MethodPost, "/v1/execute", primaryAgent.Token, map[string]any{
		"request_id": requestID, "task_id": primaryTaskID,
	})
	if status != http.StatusForbidden || harness.vault.reads.Load() != 0 || harness.targetCalls.Load() != 0 {
		t.Fatalf("unapproved execution status=%d vault_reads=%d target_calls=%d", status, harness.vault.reads.Load(), harness.targetCalls.Load())
	}

	status, authorizationsBody := owner.call(t, http.MethodGet, "/v1/web/authorizations", nil, false)
	if status != http.StatusOK {
		t.Fatalf("owner list behavior authorizations status=%d", status)
	}
	var approvals []struct {
		RequestID string `json:"request_id"`
		AgentID   string `json:"agent_id"`
		TaskID    string `json:"task_id"`
		Status    string `json:"status"`
	}
	decodeBehaviorJSON(t, authorizationsBody, &approvals)
	if len(approvals) != 1 || approvals[0].RequestID != requestID || approvals[0].AgentID != primaryAgent.ID ||
		approvals[0].TaskID != primaryTaskID || approvals[0].Status != "PENDING_APPROVAL" {
		t.Fatal("ordinary user did not see the exact pending behavior request")
	}
	status, _ = owner.call(t, http.MethodPost, "/v1/web/authorizations/"+requestID+"/decision", map[string]any{
		"decision": "APPROVED", "grant_ttl_seconds": 60,
	}, false)
	if status != http.StatusForbidden {
		t.Fatalf("approval without CSRF status=%d", status)
	}
	status, _ = owner.call(t, http.MethodPost, "/v1/web/authorizations/"+requestID+"/decision", map[string]any{
		"decision": "APPROVED", "grant_ttl_seconds": 60,
	}, true)
	if status != http.StatusOK {
		t.Fatalf("owner approve behavior authorization status=%d", status)
	}
	status, approvedStatusBody := harness.agentCall(
		t, harness.control.URL, http.MethodGet, "/v1/agent/authorizations/"+requestID, primaryAgent.Token, nil,
	)
	if status != http.StatusOK {
		t.Fatalf("approved behavior status lookup status=%d", status)
	}
	var approvedStatus struct {
		RequestStatus  string  `json:"request_status"`
		GrantStatus    *string `json:"grant_status"`
		GrantExpiresAt *string `json:"grant_expires_at"`
	}
	decodeBehaviorJSON(t, approvedStatusBody, &approvedStatus)
	if approvedStatus.RequestStatus != "APPROVED" || approvedStatus.GrantStatus == nil ||
		*approvedStatus.GrantStatus != "APPROVED" || approvedStatus.GrantExpiresAt == nil {
		t.Fatal("Agent did not observe an executable approved behavior Grant")
	}
	assertNoRuntimeValues(t, approvedStatusBody, primaryAgent.Token, harness.protectedValue, harness.vaultPath)

	status, unrelatedListBody := unrelatedUser.call(t, http.MethodGet, "/v1/web/authorizations", nil, false)
	if status != http.StatusOK {
		t.Fatalf("unrelated user list behavior authorizations status=%d", status)
	}
	var unrelatedApprovals []json.RawMessage
	decodeBehaviorJSON(t, unrelatedListBody, &unrelatedApprovals)
	if len(unrelatedApprovals) != 0 {
		t.Fatal("unrelated user could see another user's behavior request")
	}
	status, _ = unrelatedUser.call(t, http.MethodGet, "/v1/web/authorizations/"+requestID+"/audit", nil, false)
	if status != http.StatusNotFound {
		t.Fatalf("unrelated user read behavior audit status=%d", status)
	}
	status, _ = unrelatedUser.call(t, http.MethodPost, "/v1/web/authorizations/"+requestID+"/revoke", map[string]any{}, true)
	if status != http.StatusConflict {
		t.Fatalf("unrelated user revoke behavior authorization status=%d", status)
	}

	status, _ = harness.agentCall(
		t, harness.control.URL, http.MethodGet, "/v1/agent/authorizations/"+requestID, unrelatedAgent.Token, nil,
	)
	if status != http.StatusNotFound {
		t.Fatalf("unrelated Agent read behavior request status=%d", status)
	}
	status, _ = harness.agentCall(t, harness.execution.URL, http.MethodPost, "/v1/execute", unrelatedAgent.Token, map[string]any{
		"request_id": requestID, "task_id": primaryTaskID,
	})
	if status != http.StatusForbidden || harness.vault.reads.Load() != 0 || harness.targetCalls.Load() != 0 {
		t.Fatalf("cross-Agent execution status=%d vault_reads=%d target_calls=%d", status, harness.vault.reads.Load(), harness.targetCalls.Load())
	}

	wrongTaskID := beginBehaviorTask(t, harness, primaryAgent.Token)
	heartbeatBehaviorTask(t, harness, primaryAgent.Token, wrongTaskID)
	status, _ = harness.agentCall(t, harness.execution.URL, http.MethodPost, "/v1/execute", primaryAgent.Token, map[string]any{
		"request_id": requestID, "task_id": wrongTaskID,
	})
	if status != http.StatusForbidden || harness.vault.reads.Load() != 0 || harness.targetCalls.Load() != 0 {
		t.Fatalf("cross-task execution status=%d vault_reads=%d target_calls=%d", status, harness.vault.reads.Load(), harness.targetCalls.Load())
	}

	status, executionBody := harness.agentCall(t, harness.execution.URL, http.MethodPost, "/v1/execute", primaryAgent.Token, map[string]any{
		"request_id": requestID, "task_id": primaryTaskID,
	})
	if status != http.StatusOK {
		t.Fatalf("approved behavior execution status=%d", status)
	}
	var execution struct {
		OperationKind string `json:"operation_kind"`
		Result        struct {
			StatusCode int         `json:"StatusCode"`
			Headers    http.Header `json:"Headers"`
			Body       []byte      `json:"Body"`
		} `json:"result"`
	}
	decodeBehaviorJSON(t, executionBody, &execution)
	if execution.OperationKind != "HTTP" || execution.Result.StatusCode != http.StatusOK ||
		!bytes.Contains(execution.Result.Body, []byte("[REDACTED]")) ||
		!slices.Contains(execution.Result.Headers.Values("X-Behavior-Reflected"), "[REDACTED]") ||
		harness.vault.reads.Load() != 1 || harness.targetCalls.Load() != 1 || harness.invalidTargetCall.Load() != 0 {
		t.Fatalf(
			"approved execution kind=%s status=%d vault_reads=%d target_calls=%d invalid_target_calls=%d",
			execution.OperationKind, execution.Result.StatusCode, harness.vault.reads.Load(),
			harness.targetCalls.Load(), harness.invalidTargetCall.Load(),
		)
	}
	assertNoRuntimeValues(t, executionBody, harness.protectedValue, primaryAgent.Token, unrelatedAgent.Token)

	status, _ = harness.agentCall(t, harness.execution.URL, http.MethodPost, "/v1/execute", primaryAgent.Token, map[string]any{
		"request_id": requestID, "task_id": primaryTaskID,
	})
	if status != http.StatusForbidden || harness.vault.reads.Load() != 1 || harness.targetCalls.Load() != 1 {
		t.Fatalf("replay status=%d vault_reads=%d target_calls=%d", status, harness.vault.reads.Load(), harness.targetCalls.Load())
	}

	status, auditBody := owner.call(t, http.MethodGet, "/v1/web/authorizations/"+requestID+"/audit", nil, false)
	if status != http.StatusOK {
		t.Fatalf("owner read behavior audit status=%d", status)
	}
	assertBehaviorAudit(t, auditBody)
	assertNoRuntimeValues(
		t, auditBody, harness.protectedValue, harness.vaultPath, owner.password, owner.session, owner.csrf,
		primaryAgent.Token, unrelatedUser.password, unrelatedUser.session, unrelatedUser.csrf, unrelatedAgent.Token,
	)

	endBehaviorTask(t, harness, primaryAgent.Token, primaryTaskID, "COMPLETED")
	endBehaviorTask(t, harness, primaryAgent.Token, wrongTaskID, "CANCELLED")
	manifest := harness.dataManifest(t)
	if manifest != journey.ExpectedManifest {
		t.Fatalf("behavior data manifest=%+v want=%+v", manifest, journey.ExpectedManifest)
	}
	if harness.logsContain(
		harness.adminPassword, harness.protectedValue, harness.vaultPath,
		owner.password, owner.session, owner.csrf, primaryAgent.Token,
		unrelatedUser.password, unrelatedUser.session, unrelatedUser.csrf, unrelatedAgent.Token,
	) {
		t.Fatal("behavior HTTP logs exposed a runtime-only value")
	}
}

func TestEndedTaskCannotBeApproved(t *testing.T) {
	journey := actorJourneyByID(t, "ended-task-cannot-be-approved")
	harness := newBehaviorHarness(t)
	owner := harness.registerWebActor(t, "behavior-owner")
	primaryAgent := owner.registerAgent(t, "primary-agent")
	taskID := beginBehaviorTask(t, harness, primaryAgent.Token)
	heartbeatBehaviorTask(t, harness, primaryAgent.Token, taskID)
	requestID := submitBehaviorAuthorization(
		t, harness, primaryAgent.Token, taskID, harness.operationRecord.ID, harness.operationVersion.Version,
	)
	endBehaviorTask(t, harness, primaryAgent.Token, taskID, "CANCELLED")

	decisionStatus, _ := owner.call(
		t,
		http.MethodPost,
		"/v1/web/authorizations/"+requestID+"/decision",
		map[string]any{"decision": "APPROVED", "grant_ttl_seconds": 60},
		true,
	)
	manifest := harness.dataManifest(t)
	leaked := harness.logsContain(
		harness.adminPassword, harness.protectedValue, harness.vaultPath,
		owner.password, owner.session, owner.csrf, primaryAgent.Token,
	)
	if decisionStatus != http.StatusConflict || manifest != journey.ExpectedManifest ||
		harness.vault.reads.Load() != 0 || harness.targetCalls.Load() != 0 || leaked {
		t.Fatalf(
			"late approval status=%d manifest=%+v want=%+v vault_reads=%d target_calls=%d runtime_value_logged=%t",
			decisionStatus, manifest, journey.ExpectedManifest, harness.vault.reads.Load(), harness.targetCalls.Load(), leaked,
		)
	}
}

func loadActorJourneys(t *testing.T) actorJourneySet {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(actorJourneyData))
	decoder.DisallowUnknownFields()
	var result actorJourneySet
	if err := decoder.Decode(&result); err != nil {
		t.Fatal("decode behavior journey data")
	}
	return result
}

func actorJourneyByID(t *testing.T, id string) actorJourney {
	t.Helper()
	for _, journey := range loadActorJourneys(t).Journeys {
		if journey.ID == id {
			return journey
		}
	}
	t.Fatal("behavior journey data is missing an expected journey")
	return actorJourney{}
}

func beginBehaviorTask(t *testing.T, harness *behaviorHarness, token string) string {
	t.Helper()
	status, body := harness.agentCall(t, harness.control.URL, http.MethodPost, "/v1/agent/tasks", token, map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("begin behavior task status=%d", status)
	}
	var task struct {
		ID string `json:"task_id"`
	}
	decodeBehaviorJSON(t, body, &task)
	if task.ID == "" {
		t.Fatal("behavior task response omitted task_id")
	}
	return task.ID
}

func heartbeatBehaviorTask(t *testing.T, harness *behaviorHarness, token, taskID string) {
	t.Helper()
	status, _ := harness.agentCall(
		t, harness.control.URL, http.MethodPost, "/v1/agent/tasks/"+taskID+"/heartbeat", token, map[string]any{},
	)
	if status != http.StatusNoContent {
		t.Fatalf("heartbeat behavior task status=%d", status)
	}
}

func endBehaviorTask(t *testing.T, harness *behaviorHarness, token, taskID, outcome string) {
	t.Helper()
	status, _ := harness.agentCall(
		t, harness.control.URL, http.MethodPost, "/v1/agent/tasks/"+taskID+"/end", token,
		map[string]any{"outcome": outcome},
	)
	if status != http.StatusNoContent {
		t.Fatalf("end behavior task status=%d", status)
	}
}

func submitBehaviorAuthorization(
	t *testing.T,
	harness *behaviorHarness,
	token, taskID, operationID string,
	version int,
) string {
	t.Helper()
	status, body := harness.agentCall(
		t,
		harness.control.URL,
		http.MethodPost,
		"/v1/agent/authorizations",
		token,
		map[string]any{
			"task_id": taskID, "target_id": harness.targetRecord.ID,
			"operation_id": operationID, "version": version,
			"arguments": map[string]any{}, "reason": "behavior journey requires one protected operation",
		},
	)
	if status != http.StatusCreated {
		t.Fatalf("submit behavior authorization status=%d", status)
	}
	var authorizationResponse struct {
		RequestID string `json:"request_id"`
		Status    string `json:"status"`
	}
	decodeBehaviorJSON(t, body, &authorizationResponse)
	if authorizationResponse.RequestID == "" || authorizationResponse.Status != "PENDING_APPROVAL" {
		t.Fatal("behavior authorization did not enter pending approval")
	}
	return authorizationResponse.RequestID
}

func assertBehaviorAudit(t *testing.T, body []byte) {
	t.Helper()
	var events []struct {
		EventType string `json:"event_type"`
	}
	decodeBehaviorJSON(t, body, &events)
	present := make(map[string]bool)
	for _, event := range events {
		present[event.EventType] = true
	}
	for _, required := range []string{
		"authorization_requests.insert",
		"approvals.insert",
		"operation_grants.insert",
		"executions.insert",
		"reclaims.insert",
		"operation_grants.claim_denied",
	} {
		if !present[required] {
			t.Fatalf("behavior audit is missing event type %s", required)
		}
	}
}
