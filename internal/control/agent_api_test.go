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

func testAgentServer(authorizations *apiAuthorizations) *http.Server {
	runtime := &AgentRuntime{Authenticator: apiAuthenticator{}, Targets: apiTargets{}, Tasks: apiTasks{}, Authorizations: authorizations, Statuses: apiStatuses{}}
	return NewServer(Config{ListenAddress: "127.0.0.1:0"}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), runtime, nil)
}
