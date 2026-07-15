package control

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/identity"
	"github.com/fallingnight/akv/internal/lifecycle"
)

type ApprovalView struct {
	RequestID        string          `json:"request_id"`
	AgentID          string          `json:"agent_id"`
	AgentName        string          `json:"agent_name"`
	OwnerUsername    string          `json:"owner_username"`
	TaskID           string          `json:"task_id"`
	TargetID         string          `json:"target_id"`
	TargetName       string          `json:"target_name"`
	CredentialAlias  string          `json:"credential_alias"`
	CredentialType   string          `json:"credential_type"`
	OperationID      string          `json:"operation_id,omitempty"`
	OperationVersion int             `json:"version,omitempty"`
	OperationKey     string          `json:"operation_key,omitempty"`
	OperationName    string          `json:"operation_name,omitempty"`
	OperationKind    string          `json:"operation_kind"`
	Arguments        json.RawMessage `json:"arguments,omitempty"`
	Operation        json.RawMessage `json:"operation"`
	Reason           string          `json:"reason"`
	Status           string          `json:"status"`
	CreatedAt        time.Time       `json:"created_at"`
	ApprovalDeadline time.Time       `json:"approval_deadline"`
	GrantExpiresAt   *time.Time      `json:"grant_expires_at,omitempty"`
	RiskHint         string          `json:"risk_hint"`
	RiskLevel        string          `json:"risk_level,omitempty"`
}
type AuditView struct {
	ID          string            `json:"id"`
	EventType   string            `json:"event_type"`
	ActorType   string            `json:"actor_type"`
	ActorID     *string           `json:"actor_id,omitempty"`
	RequestID   *string           `json:"request_id,omitempty"`
	ApprovalID  *string           `json:"approval_id,omitempty"`
	GrantID     *string           `json:"grant_id,omitempty"`
	ExecutionID *string           `json:"execution_id,omitempty"`
	ReclaimID   *string           `json:"reclaim_id,omitempty"`
	Metadata    map[string]string `json:"metadata"`
	CreatedAt   time.Time         `json:"created_at"`
}
type IncidentView struct {
	ID         string     `json:"id"`
	ReclaimID  *string    `json:"reclaim_id,omitempty"`
	Status     string     `json:"status"`
	ErrorCode  string     `json:"error_code"`
	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

type WebApprovalReader interface {
	ListAuthorizationRequests(context.Context, identity.User) ([]ApprovalView, error)
	ReadAuthorizationAudit(context.Context, identity.User, string) ([]AuditView, error)
	ListAuditEvents(context.Context, identity.User) ([]AuditView, error)
	ListSecurityIncidents(context.Context, identity.User) ([]IncidentView, error)
	ResolveSecurityIncident(context.Context, identity.User, string) error
}

func (runtime *WebRuntime) listAuditEvents(response http.ResponseWriter, request *http.Request) {
	actor, _, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	events, err := runtime.ApprovalReader.ListAuditEvents(request.Context(), actor)
	if err != nil {
		writeJSON(response, http.StatusForbidden, map[string]string{"error": "FORBIDDEN"})
		return
	}
	writeJSON(response, http.StatusOK, events)
}

type WebApprovalDecider interface {
	Decide(context.Context, identity.User, string, authorization.Decision, *time.Duration) (authorization.Approval, *authorization.Grant, error)
}
type WebRevoker interface {
	Revoke(context.Context, identity.User, string) (lifecycle.RevokeResult, error)
}

func (runtime *WebRuntime) listAuthorizations(response http.ResponseWriter, request *http.Request) {
	actor, _, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	records, err := runtime.ApprovalReader.ListAuthorizationRequests(request.Context(), actor)
	if err != nil {
		writeJSON(response, 500, map[string]string{"error": "INTERNAL"})
		return
	}
	writeJSON(response, 200, records)
}
func (runtime *WebRuntime) decideAuthorization(response http.ResponseWriter, request *http.Request) {
	actor, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	var input struct {
		Decision        authorization.Decision `json:"decision"`
		GrantTTLSeconds *int64                 `json:"grant_ttl_seconds,omitempty"`
	}
	if !decodeStrict(response, request, &input) {
		return
	}
	var ttl *time.Duration
	if input.GrantTTLSeconds != nil {
		value := time.Duration(*input.GrantTTLSeconds) * time.Second
		ttl = &value
	}
	approval, grant, err := runtime.Approvals.Decide(request.Context(), actor, request.PathValue("request_id"), input.Decision, ttl)
	if err != nil {
		writeJSON(response, http.StatusConflict, map[string]string{"error": "DECISION_REJECTED"})
		return
	}
	result := map[string]any{"approval_id": approval.ID, "decision": approval.Decision}
	if grant != nil {
		result["grant_expires_at"] = grant.ExpiresAt
	}
	writeJSON(response, http.StatusOK, result)
}
func (runtime *WebRuntime) revokeAuthorization(response http.ResponseWriter, request *http.Request) {
	actor, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	var input struct{}
	if !decodeStrict(response, request, &input) {
		return
	}
	result, err := runtime.Revocations.Revoke(request.Context(), actor, request.PathValue("request_id"))
	if err != nil {
		writeJSON(response, http.StatusConflict, map[string]string{"error": "REVOCATION_REJECTED"})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"revoked_before_execution": result.RevokedBeforeExecution, "cancellation_requested": len(result.CancelExecutionIDs) > 0})
}
func (runtime *WebRuntime) authorizationAudit(response http.ResponseWriter, request *http.Request) {
	actor, _, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	events, err := runtime.ApprovalReader.ReadAuthorizationAudit(request.Context(), actor, request.PathValue("request_id"))
	if err != nil {
		writeJSON(response, http.StatusNotFound, map[string]string{"error": "NOT_FOUND"})
		return
	}
	writeJSON(response, http.StatusOK, events)
}
func (runtime *WebRuntime) listIncidents(response http.ResponseWriter, request *http.Request) {
	actor, _, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	incidents, err := runtime.ApprovalReader.ListSecurityIncidents(request.Context(), actor)
	if err != nil {
		writeJSON(response, http.StatusForbidden, map[string]string{"error": "FORBIDDEN"})
		return
	}
	writeJSON(response, http.StatusOK, incidents)
}

func (runtime *WebRuntime) resolveIncident(response http.ResponseWriter, request *http.Request) {
	actor, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	var input struct{}
	if !decodeStrict(response, request, &input) {
		return
	}
	if err := runtime.ApprovalReader.ResolveSecurityIncident(request.Context(), actor, request.PathValue("incident_id")); err != nil {
		writeJSON(response, http.StatusForbidden, map[string]string{"error": "FORBIDDEN"})
		return
	}
	response.WriteHeader(http.StatusNoContent)
}
