package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fallingnight/akv/internal/agent"
	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/domain"
	"github.com/fallingnight/akv/internal/lifecycle"
	"github.com/fallingnight/akv/internal/task"
)

type AgentAuthenticator interface {
	Authenticate(context.Context, string) (agent.Principal, error)
}
type TargetDiscovery interface {
	Discover(context.Context, string) ([]catalog.Target, error)
}
type OperationDiscovery interface {
	DiscoverOperations(context.Context, string, string) ([]catalog.PublicOperation, error)
}
type TaskManager interface {
	Begin(context.Context, string) (task.Record, error)
	Heartbeat(context.Context, string, string) error
	End(context.Context, string, string, domain.TaskStatus) ([]string, error)
}
type AuthorizationSubmitter interface {
	Submit(context.Context, agent.Principal, authorization.SubmitInput) (authorization.Request, error)
}

type AuthorizationStatus struct {
	RequestID        string     `json:"request_id"`
	RequestStatus    string     `json:"request_status"`
	ApprovalDeadline time.Time  `json:"approval_deadline"`
	Decision         *string    `json:"decision,omitempty"`
	GrantStatus      *string    `json:"grant_status,omitempty"`
	GrantExpiresAt   *time.Time `json:"grant_expires_at,omitempty"`
	ExecutionStatus  *string    `json:"execution_status,omitempty"`
	ReclaimStatus    *string    `json:"reclaim_status,omitempty"`
	ErrorCode        *string    `json:"error_code,omitempty"`
	OperationKind    string     `json:"operation_kind"`
	OperationID      string     `json:"operation_id,omitempty"`
	OperationVersion int        `json:"version,omitempty"`
}
type StatusReader interface {
	GetAuthorizationStatus(context.Context, string, string) (AuthorizationStatus, error)
}
type AgentRevoker interface {
	RevokeAgent(context.Context, agent.Principal, string) (lifecycle.RevokeResult, error)
}

type AgentRuntime struct {
	Authenticator  AgentAuthenticator
	Targets        TargetDiscovery
	Operations     OperationDiscovery
	Tasks          TaskManager
	Authorizations AuthorizationSubmitter
	Statuses       StatusReader
	Revocations    AgentRevoker
}

func (runtime *AgentRuntime) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/agent/targets", runtime.listTargets)
	if runtime.Operations != nil {
		mux.HandleFunc("GET /v1/agent/targets/{target_id}/operations", runtime.listOperations)
	}
	mux.HandleFunc("POST /v1/agent/tasks", runtime.beginTask)
	mux.HandleFunc("POST /v1/agent/tasks/{task_id}/heartbeat", runtime.heartbeat)
	mux.HandleFunc("POST /v1/agent/tasks/{task_id}/end", runtime.endTask)
	mux.HandleFunc("POST /v1/agent/authorizations", runtime.requestAuthorization)
	mux.HandleFunc("GET /v1/agent/authorizations/{request_id}", runtime.authorizationStatus)
	if runtime.Revocations != nil {
		mux.HandleFunc("POST /v1/agent/authorizations/{request_id}/revoke", runtime.revokeAuthorization)
	}
}

func (runtime *AgentRuntime) authenticate(response http.ResponseWriter, request *http.Request) (agent.Principal, bool) {
	header := request.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "UNAUTHORIZED"})
		return agent.Principal{}, false
	}
	principal, err := runtime.Authenticator.Authenticate(request.Context(), strings.TrimPrefix(header, "Bearer "))
	request.Header.Del("Authorization")
	if err != nil {
		writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "UNAUTHORIZED"})
		return agent.Principal{}, false
	}
	return principal, true
}

func (runtime *AgentRuntime) listTargets(response http.ResponseWriter, request *http.Request) {
	principal, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	targets, err := runtime.Targets.Discover(request.Context(), principal.AgentID)
	if err != nil {
		writeJSON(response, 500, map[string]string{"error": "INTERNAL"})
		return
	}
	type dto struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Description   string `json:"description"`
		Connector     string `json:"connector_type"`
		OperationsURL string `json:"operations_url"`
	}
	result := make([]dto, 0, len(targets))
	for _, target := range targets {
		result = append(result, dto{target.ID, target.Name, target.Description, string(target.ConnectorType), "/v1/agent/targets/" + target.ID + "/operations"})
	}
	writeJSON(response, 200, result)
}

func (runtime *AgentRuntime) listOperations(response http.ResponseWriter, request *http.Request) {
	principal, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	targetID := request.PathValue("target_id")
	operations, err := runtime.Operations.DiscoverOperations(request.Context(), principal.AgentID, targetID)
	if err != nil {
		writeJSON(response, http.StatusNotFound, map[string]string{"error": "TARGET_NOT_FOUND"})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"target_id": targetID, "operations": operations})
}
func (runtime *AgentRuntime) beginTask(response http.ResponseWriter, request *http.Request) {
	principal, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	var input struct{}
	if !decodeStrict(response, request, &input) {
		return
	}
	record, err := runtime.Tasks.Begin(request.Context(), principal.AgentID)
	if err != nil {
		writeJSON(response, 500, map[string]string{"error": "INTERNAL"})
		return
	}
	writeJSON(response, 201, map[string]any{"task_id": record.ID, "status": record.Status, "heartbeat_interval_seconds": int(task.HeartbeatInterval.Seconds())})
}
func (runtime *AgentRuntime) heartbeat(response http.ResponseWriter, request *http.Request) {
	principal, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	var input struct{}
	if !decodeStrict(response, request, &input) {
		return
	}
	if err := runtime.Tasks.Heartbeat(request.Context(), principal.AgentID, request.PathValue("task_id")); err != nil {
		writeJSON(response, 403, map[string]string{"error": "TASK_UNAVAILABLE"})
		return
	}
	response.WriteHeader(http.StatusNoContent)
}
func (runtime *AgentRuntime) endTask(response http.ResponseWriter, request *http.Request) {
	principal, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	var input struct {
		Outcome domain.TaskStatus `json:"outcome"`
	}
	if !decodeStrict(response, request, &input) {
		return
	}
	if _, err := runtime.Tasks.End(request.Context(), principal.AgentID, request.PathValue("task_id"), input.Outcome); err != nil {
		writeJSON(response, 403, map[string]string{"error": "TASK_UNAVAILABLE"})
		return
	}
	response.WriteHeader(http.StatusNoContent)
}
func (runtime *AgentRuntime) requestAuthorization(response http.ResponseWriter, request *http.Request) {
	principal, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	var input authorization.SubmitInput
	if !decodeStrict(response, request, &input) {
		return
	}
	record, err := runtime.Authorizations.Submit(request.Context(), principal, input)
	if err != nil {
		if errors.Is(err, authorization.ErrInvalidRequest) {
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_ARGUMENTS"})
		} else {
			writeJSON(response, http.StatusForbidden, map[string]string{"error": "OPERATION_UNAVAILABLE"})
		}
		return
	}
	writeJSON(response, 201, map[string]any{"request_id": record.ID, "status": record.Status, "approval_deadline": record.ApprovalDeadline})
}
func (runtime *AgentRuntime) authorizationStatus(response http.ResponseWriter, request *http.Request) {
	principal, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	status, err := runtime.Statuses.GetAuthorizationStatus(request.Context(), principal.AgentID, request.PathValue("request_id"))
	if err != nil {
		writeJSON(response, 404, map[string]string{"error": "NOT_FOUND"})
		return
	}
	writeJSON(response, 200, status)
}

func (runtime *AgentRuntime) revokeAuthorization(response http.ResponseWriter, request *http.Request) {
	principal, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	var input struct{}
	if !decodeStrict(response, request, &input) {
		return
	}
	result, err := runtime.Revocations.RevokeAgent(request.Context(), principal, request.PathValue("request_id"))
	if err != nil {
		writeJSON(response, http.StatusConflict, map[string]string{"error": "REVOCATION_REJECTED"})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"revoked_before_execution": result.RevokedBeforeExecution, "cancellation_requested": len(result.CancelExecutionIDs) > 0})
}

func decodeStrict(response http.ResponseWriter, request *http.Request, destination any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeJSON(response, 400, map[string]string{"error": "INVALID_REQUEST"})
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeJSON(response, 400, map[string]string{"error": "INVALID_REQUEST"})
		return false
	}
	return true
}
func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
