package proxy

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fallingnight/akv/internal/agent"
)

type serverAuthenticator struct{}

func (serverAuthenticator) Authenticate(context.Context, string) (agent.Principal, error) {
	return agent.Principal{AgentID: "agent"}, nil
}

type serverHTTPExecutor struct{ calls int }

func (executor *serverHTTPExecutor) Execute(_ context.Context, requestID, agentID, taskID string) (HTTPResult, error) {
	executor.calls++
	return HTTPResult{StatusCode: 204, Body: []byte(requestID + agentID + taskID)}, nil
}

func TestExecutionServerHealthIdentifiesProcessBoundary(t *testing.T) {
	server := NewRuntimeServer(ServerConfig{ListenAddress: defaultListenAddress}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK || response.Body.String() != "{\"service\":\"akv-execution-proxy\",\"status\":\"ok\"}\n" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestExecutionRouteAuthenticatesAndAcceptsOnlyIdentifiers(t *testing.T) {
	executor := &serverHTTPExecutor{}
	runtime := &Runtime{Authenticator: serverAuthenticator{}, HTTP: executor}
	server := NewRuntimeServer(ServerConfig{ListenAddress: defaultListenAddress}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), runtime)

	request := httptest.NewRequest(http.MethodPost, "/v1/execute/http", bytes.NewBufferString(`{"request_id":"request","task_id":"task"}`))
	request.Header.Set("Authorization", "Bearer fixture-agent-token")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || executor.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%q", response.Code, executor.calls, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/execute/http", bytes.NewBufferString(`{"request_id":"request","task_id":"task","target_id":"attacker-target"}`))
	request.Header.Set("Authorization", "Bearer fixture-agent-token")
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || executor.calls != 1 {
		t.Fatalf("unknown-field status=%d calls=%d", response.Code, executor.calls)
	}
}

func TestExecutionRouteRejectsMissingBearerBeforeExecutor(t *testing.T) {
	executor := &serverHTTPExecutor{}
	server := NewRuntimeServer(ServerConfig{ListenAddress: defaultListenAddress}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), &Runtime{Authenticator: serverAuthenticator{}, HTTP: executor})
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/execute/http", bytes.NewBufferString(`{"request_id":"request","task_id":"task"}`)))
	if response.Code != http.StatusUnauthorized || executor.calls != 0 {
		t.Fatalf("status=%d calls=%d", response.Code, executor.calls)
	}
}
