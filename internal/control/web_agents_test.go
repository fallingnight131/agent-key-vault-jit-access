package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/agent"
)

type webAgentsFake struct {
	records       []agent.View
	revokeCalls   int
	revokeOwnerID string
	revokeAgentID string
	revokeErr     error
}

func (fake *webAgentsFake) List(_ context.Context, ownerID string) ([]agent.View, error) {
	if ownerID != "user" {
		return nil, agent.ErrForbidden
	}
	return fake.records, nil
}
func (fake *webAgentsFake) Register(_ context.Context, ownerID, name string, _ agent.TokenLifetime) (agent.Credential, error) {
	if ownerID != "user" {
		return agent.Credential{}, agent.ErrForbidden
	}
	fake.records = append(fake.records, agent.View{ID: "agent", Name: name, Active: true, CreatedAt: time.Now(), HasActiveToken: true})
	return agent.Credential{AgentID: "agent", Token: "one-time-agent-token"}, nil
}
func (fake *webAgentsFake) RotateToken(context.Context, string, string, agent.TokenLifetime) (agent.Credential, error) {
	return agent.Credential{AgentID: "agent", Token: "replacement-agent-token"}, nil
}
func (fake *webAgentsFake) RevokeToken(_ context.Context, ownerID, agentID string) error {
	fake.revokeCalls++
	fake.revokeOwnerID = ownerID
	fake.revokeAgentID = agentID
	return fake.revokeErr
}
func (fake *webAgentsFake) SetActive(context.Context, string, string, bool) error { return nil }

func TestWebAgentRegistrationReturnsTokenOnce(t *testing.T) {
	agents := &webAgentsFake{}
	server := testWebAgentServer(agents)
	request := authenticatedWebRequest(http.MethodPost, "/v1/web/agents", `{"name":"worker","token_lifetime":"24_HOURS"}`)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), "one-time-agent-token") {
		t.Fatalf("register status=%d body=%q", response.Code, response.Body.String())
	}

	request = authenticatedWebRequest(http.MethodGet, "/v1/web/agents", "")
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "one-time-agent-token") || !strings.Contains(response.Body.String(), `"has_active_token":true`) {
		t.Fatalf("list status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestWebAgentTokenRevocation(t *testing.T) {
	agents := &webAgentsFake{}
	server := testWebAgentServer(agents)
	request := authenticatedWebRequest(http.MethodDelete, "/v1/web/agents/agent-id/token", "")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || agents.revokeCalls != 1 || agents.revokeOwnerID != "user" || agents.revokeAgentID != "agent-id" {
		t.Fatalf("status=%d calls=%d owner=%q agent=%q body=%q", response.Code, agents.revokeCalls, agents.revokeOwnerID, agents.revokeAgentID, response.Body.String())
	}
}

func TestWebAgentTokenRevocationMapsErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "forbidden", err: fmt.Errorf("revoke agent token: %w", agent.ErrForbidden), wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN"},
		{name: "internal", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			agents := &webAgentsFake{revokeErr: test.err}
			server := testWebAgentServer(agents)
			request := authenticatedWebRequest(http.MethodDelete, "/v1/web/agents/agent-id/token", "")
			response := httptest.NewRecorder()
			server.Handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), `"error":"`+test.wantCode+`"`) {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
}

func TestWebAgentMutationRequiresCSRF(t *testing.T) {
	agents := &webAgentsFake{}
	server := testWebAgentServer(agents)
	request := httptest.NewRequest(http.MethodPost, "/v1/web/agents", strings.NewReader(`{"name":"worker","token_lifetime":"24_HOURS"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session-secret"})
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || len(agents.records) != 0 {
		t.Fatalf("status=%d records=%d", response.Code, len(agents.records))
	}

	request = httptest.NewRequest(http.MethodDelete, "/v1/web/agents/agent-id/token", nil)
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session-secret"})
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || agents.revokeCalls != 0 || !strings.Contains(response.Body.String(), `"error":"CSRF_REJECTED"`) {
		t.Fatalf("delete status=%d calls=%d body=%q", response.Code, agents.revokeCalls, response.Body.String())
	}
}

func authenticatedWebRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session-secret"})
	request.AddCookie(&http.Cookie{Name: webCSRFCookie, Value: "csrf-token"})
	request.Header.Set("X-AKV-CSRF", "csrf-token")
	return request
}

func testWebAgentServer(agents WebAgentManager) *http.Server {
	config := Config{ListenAddress: "127.0.0.1:0", PublicOrigin: "https://akv.example.test"}
	return NewServer(config, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), nil, &WebRuntime{Identity: &webIdentityFake{}, Agents: agents})
}
