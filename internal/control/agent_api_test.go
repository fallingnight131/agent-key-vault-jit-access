package control

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/agent"
	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/domain"
	"github.com/fallingnight/akv/internal/lifecycle"
	"github.com/fallingnight/akv/internal/task"
)

type apiAuthenticator struct{}

func (apiAuthenticator) Authenticate(context.Context, string) (agent.Principal, error) {
	return agent.Principal{AgentID: "agent"}, nil
}

type apiTargets struct{}

func (apiTargets) Discover(context.Context, string) ([]catalog.Target, error) {
	return []catalog.Target{{ID: "target", Name: "tickets", Description: "safe", ConnectorType: catalog.ConnectorHTTP, DefaultCredentialID: "must-not-leak", ConnectionConfig: catalog.ConnectionConfig{BaseURL: "https://internal.example", AllowedHTTPMethods: []string{"POST"}}}}, nil
}

type apiTasks struct{}

func (apiTasks) Begin(context.Context, string) (task.Record, error) {
	return task.Record{ID: "task", Status: domain.TaskActive}, nil
}
func (apiTasks) Heartbeat(context.Context, string, string) error { return nil }
func (apiTasks) End(context.Context, string, string, domain.TaskStatus) ([]string, error) {
	return nil, nil
}

type apiAuthorizations struct{ calls int }

func (authorizations *apiAuthorizations) Submit(context.Context, agent.Principal, authorization.SubmitInput) (authorization.Request, error) {
	authorizations.calls++
	return authorization.Request{}, errors.New("unused")
}

type apiStatuses struct{}

func (apiStatuses) GetAuthorizationStatus(context.Context, string, string) (AuthorizationStatus, error) {
	return AuthorizationStatus{RequestID: "request", RequestStatus: "PENDING_APPROVAL"}, nil
}

type apiRevocations struct{ calls int }

func (revocations *apiRevocations) RevokeAgent(context.Context, agent.Principal, string) (lifecycle.RevokeResult, error) {
	revocations.calls++
	return lifecycle.RevokeResult{RevokedBeforeExecution: true}, nil
}

type directAPIAuthenticator struct{ tokens []string }

func (authenticator *directAPIAuthenticator) Authenticate(_ context.Context, token string) (agent.Principal, error) {
	authenticator.tokens = append(authenticator.tokens, token)
	return agent.Principal{AgentID: "agent"}, nil
}

type directAPITasks struct {
	heartbeats int
	ended      bool
	outcome    domain.TaskStatus
}

func (*directAPITasks) Begin(context.Context, string) (task.Record, error) {
	return task.Record{ID: "task", Status: domain.TaskActive}, nil
}
func (tasks *directAPITasks) Heartbeat(_ context.Context, agentID, taskID string) error {
	if agentID != "agent" || taskID != "task" {
		return errors.New("unexpected heartbeat binding")
	}
	tasks.heartbeats++
	return nil
}
func (tasks *directAPITasks) End(_ context.Context, agentID, taskID string, outcome domain.TaskStatus) ([]string, error) {
	if agentID != "agent" || taskID != "task" {
		return nil, errors.New("unexpected end binding")
	}
	tasks.ended, tasks.outcome = true, outcome
	return nil, nil
}

type directAPIAuthorizations struct {
	calls int
	input authorization.SubmitInput
}

func (authorizations *directAPIAuthorizations) Submit(_ context.Context, principal agent.Principal, input authorization.SubmitInput) (authorization.Request, error) {
	if principal.AgentID != "agent" {
		return authorization.Request{}, errors.New("unexpected principal")
	}
	authorizations.calls++
	authorizations.input = input
	return authorization.Request{ID: "request", Status: domain.RequestPendingApproval, ApprovalDeadline: time.Now().Add(time.Minute)}, nil
}

func TestAgentTargetsDTOExcludesInternalCredentialAndConnection(t *testing.T) {
	server := testAgentServer(&apiAuthorizations{})
	request := httptest.NewRequest(http.MethodGet, "/v1/agent/targets", nil)
	request.Header.Set("Authorization", "Bearer fixture-token")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != 200 || strings.Contains(body, "must-not-leak") || strings.Contains(body, "internal.example") || strings.Contains(body, "vault") {
		t.Fatalf("status=%d body=%q", response.Code, body)
	}
}

func TestAuthorizationRequestRejectsCredentialAndTargetBypassFields(t *testing.T) {
	authorizations := &apiAuthorizations{}
	server := testAgentServer(authorizations)
	body := `{"task_id":"task","target_id":"target","credential_id":"attacker","operation":{"kind":"HTTP","http":{"method":"POST","path":"/"}},"reason":"fixture"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/agent/authorizations", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer fixture-token")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || authorizations.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%q", response.Code, authorizations.calls, response.Body.String())
	}
}

func TestAgentAPIRequiresBearer(t *testing.T) {
	server := testAgentServer(&apiAuthorizations{})
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/agent/targets", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestAgentCanRevokeOwnedAuthorizationWithoutTokenInPayload(t *testing.T) {
	revocations := &apiRevocations{}
	runtime := &AgentRuntime{Authenticator: apiAuthenticator{}, Targets: apiTargets{}, Tasks: apiTasks{}, Authorizations: &apiAuthorizations{}, Statuses: apiStatuses{}, Revocations: revocations}
	server := NewServer(Config{ListenAddress: "127.0.0.1:0"}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), runtime, nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/agent/authorizations/request/revoke", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer fixture-token")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || revocations.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%q", response.Code, revocations.calls, response.Body.String())
	}
}

func TestAgentBearerAPILifecycleDoesNotEchoToken(t *testing.T) {
	const token = "direct-agent-token-must-not-leak"
	authenticator := &directAPIAuthenticator{}
	tasks := &directAPITasks{}
	authorizations := &directAPIAuthorizations{}
	var logs bytes.Buffer
	runtime := &AgentRuntime{
		Authenticator:  authenticator,
		Targets:        apiTargets{},
		Tasks:          tasks,
		Authorizations: authorizations,
		Statuses:       apiStatuses{},
	}
	server := NewServer(Config{ListenAddress: "127.0.0.1:0"}, slog.New(slog.NewJSONHandler(&logs, nil)), runtime, nil)
	var responses strings.Builder
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, request)
		responses.WriteString(response.Body.String())
		return response
	}

	if response := call(http.MethodGet, "/v1/agent/targets", ""); response.Code != http.StatusOK {
		t.Fatalf("targets status=%d body=%q", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/v1/agent/tasks", `{}`); response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"heartbeat_interval_seconds":15`) {
		t.Fatalf("begin status=%d body=%q", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/v1/agent/tasks/task/heartbeat", `{}`); response.Code != http.StatusNoContent {
		t.Fatalf("heartbeat status=%d body=%q", response.Code, response.Body.String())
	}
	input := `{"task_id":"task","target_id":"target","operation":{"kind":"HTTP","http":{"method":"POST","path":"/tickets"}},"reason":"direct API fixture"}`
	if response := call(http.MethodPost, "/v1/agent/authorizations", input); response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"request_id":"request"`) {
		t.Fatalf("authorization status=%d body=%q", response.Code, response.Body.String())
	}
	if response := call(http.MethodGet, "/v1/agent/authorizations/request", ""); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"request_status":"PENDING_APPROVAL"`) {
		t.Fatalf("status status=%d body=%q", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/v1/agent/tasks/task/end", `{"outcome":"COMPLETED"}`); response.Code != http.StatusNoContent {
		t.Fatalf("end status=%d body=%q", response.Code, response.Body.String())
	}

	if len(authenticator.tokens) != 6 {
		t.Fatalf("authentication calls=%d", len(authenticator.tokens))
	}
	for _, received := range authenticator.tokens {
		if received != token {
			t.Fatalf("unexpected token received")
		}
	}
	if tasks.heartbeats != 1 || !tasks.ended || tasks.outcome != domain.TaskCompleted {
		t.Fatalf("task lifecycle heartbeats=%d ended=%t outcome=%s", tasks.heartbeats, tasks.ended, tasks.outcome)
	}
	if authorizations.calls != 1 || authorizations.input.TaskID != "task" || authorizations.input.TargetID != "target" {
		t.Fatalf("authorization calls=%d input=%+v", authorizations.calls, authorizations.input)
	}
	if exposed := responses.String() + logs.String(); strings.Contains(exposed, token) {
		t.Fatal("Agent Token appeared in API response or request log")
	}
}

func testAgentServer(authorizations *apiAuthorizations) *http.Server {
	runtime := &AgentRuntime{Authenticator: apiAuthenticator{}, Targets: apiTargets{}, Tasks: apiTasks{}, Authorizations: authorizations, Statuses: apiStatuses{}}
	return NewServer(Config{ListenAddress: "127.0.0.1:0"}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), runtime, nil)
}
