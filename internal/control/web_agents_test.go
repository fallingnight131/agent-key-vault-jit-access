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

	"github.com/fallingnight/akv/internal/agent"
)

type webAgentsFake struct {
	records []agent.View
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
	fake.records = append(fake.records, agent.View{ID: "agent", Name: name, Active: true, CreatedAt: time.Now()})
	return agent.Credential{AgentID: "agent", Token: "one-time-agent-token"}, nil
}
func (fake *webAgentsFake) RotateToken(context.Context, string, string, agent.TokenLifetime) (agent.Credential, error) {
	return agent.Credential{AgentID: "agent", Token: "replacement-agent-token"}, nil
}
func (fake *webAgentsFake) RevokeToken(context.Context, string, string) error     { return nil }
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
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "token") {
		t.Fatalf("list status=%d body=%q", response.Code, response.Body.String())
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
