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

	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/identity"
	"github.com/fallingnight/akv/internal/lifecycle"
)

type webApprovalFake struct {
	decided bool
	revoked bool
}

func (fake *webApprovalFake) ListAuthorizationRequests(context.Context, identity.User) ([]ApprovalView, error) {
	return []ApprovalView{{RequestID: "request", AgentID: "agent", TaskID: "task", TargetName: "tickets", CredentialAlias: "default", OperationKind: "HTTP", Operation: []byte(`{"kind":"HTTP"}`), Reason: "fixture", Status: "PENDING_APPROVAL", RiskHint: "review"}}, nil
}
func (fake *webApprovalFake) ReadAuthorizationAudit(context.Context, identity.User, string) ([]AuditView, error) {
	return []AuditView{{ID: "event", EventType: "REQUEST_CREATED", Metadata: map[string]string{"status": "PENDING_APPROVAL"}}}, nil
}
func (fake *webApprovalFake) ListSecurityIncidents(context.Context, identity.User) ([]IncidentView, error) {
	return nil, nil
}
func (fake *webApprovalFake) ResolveSecurityIncident(context.Context, identity.User, string) error {
	return nil
}
func (fake *webApprovalFake) Decide(_ context.Context, _ identity.User, requestID string, decision authorization.Decision, ttl *time.Duration) (authorization.Approval, *authorization.Grant, error) {
	fake.decided = requestID == "request" && decision == authorization.DecisionApproved && ttl != nil && *ttl == time.Minute
	return authorization.Approval{ID: "approval", Decision: decision}, &authorization.Grant{ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (fake *webApprovalFake) Revoke(context.Context, identity.User, string) (lifecycle.RevokeResult, error) {
	fake.revoked = true
	return lifecycle.RevokeResult{RevokedBeforeExecution: true}, nil
}

func TestWebApprovalDecisionAndAudit(t *testing.T) {
	fake := &webApprovalFake{}
	config := Config{ListenAddress: "127.0.0.1:0", PublicOrigin: "https://akv.example.test"}
	server := NewServer(config, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), nil, &WebRuntime{Identity: &webIdentityFake{}, ApprovalReader: fake, Approvals: fake, Revocations: fake})
	request := authenticatedWebRequest(http.MethodGet, "/v1/web/authorizations", "")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != 200 || strings.Contains(response.Body.String(), "vault") {
		t.Fatalf("list status=%d body=%q", response.Code, response.Body.String())
	}
	request = authenticatedWebRequest(http.MethodPost, "/v1/web/authorizations/request/decision", `{"decision":"APPROVED","grant_ttl_seconds":60}`)
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != 200 || !fake.decided {
		t.Fatalf("decision status=%d decided=%t body=%q", response.Code, fake.decided, response.Body.String())
	}
	request = authenticatedWebRequest(http.MethodPost, "/v1/web/authorizations/request/revoke", `{}`)
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != 200 || !fake.revoked {
		t.Fatalf("revoke status=%d revoked=%t", response.Code, fake.revoked)
	}
}
