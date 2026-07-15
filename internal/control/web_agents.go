package control

import (
	"context"
	"net/http"
	"time"

	"github.com/fallingnight/akv/internal/agent"
	"github.com/fallingnight/akv/internal/identity"
)

type WebAgentManager interface {
	List(context.Context, string) ([]agent.View, error)
	Register(context.Context, string, string, agent.TokenLifetime) (agent.Credential, error)
	RotateToken(context.Context, string, string, agent.TokenLifetime) (agent.Credential, error)
	RevokeToken(context.Context, string, string) error
	SetActive(context.Context, string, string, bool) error
}

type agentDTO struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Active         bool       `json:"active"`
	CreatedAt      time.Time  `json:"created_at"`
	TokenExpiresAt *time.Time `json:"token_expires_at,omitempty"`
}

func (runtime *WebRuntime) listAgents(response http.ResponseWriter, request *http.Request) {
	user, _, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	records, err := runtime.Agents.List(request.Context(), user.ID)
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]string{"error": "INTERNAL"})
		return
	}
	result := make([]agentDTO, 0, len(records))
	for _, record := range records {
		result = append(result, agentDTO{record.ID, record.Name, record.Active, record.CreatedAt, record.TokenExpiresAt})
	}
	writeJSON(response, http.StatusOK, result)
}

func (runtime *WebRuntime) registerAgent(response http.ResponseWriter, request *http.Request) {
	user, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	var input struct {
		Name     string              `json:"name"`
		Lifetime agent.TokenLifetime `json:"token_lifetime"`
	}
	if !decodeStrict(response, request, &input) {
		return
	}
	credential, err := runtime.Agents.Register(request.Context(), user.ID, input.Name, input.Lifetime)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_REQUEST"})
		return
	}
	writeJSON(response, http.StatusCreated, credentialDTO(credential))
}

func (runtime *WebRuntime) rotateAgentToken(response http.ResponseWriter, request *http.Request) {
	user, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	var input struct {
		Lifetime agent.TokenLifetime `json:"token_lifetime"`
	}
	if !decodeStrict(response, request, &input) {
		return
	}
	credential, err := runtime.Agents.RotateToken(request.Context(), user.ID, request.PathValue("agent_id"), input.Lifetime)
	if err != nil {
		writeJSON(response, http.StatusForbidden, map[string]string{"error": "FORBIDDEN"})
		return
	}
	writeJSON(response, http.StatusOK, credentialDTO(credential))
}

func (runtime *WebRuntime) revokeAgentToken(response http.ResponseWriter, request *http.Request) {
	user, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	if err := runtime.Agents.RevokeToken(request.Context(), user.ID, request.PathValue("agent_id")); err != nil {
		writeJSON(response, http.StatusForbidden, map[string]string{"error": "FORBIDDEN"})
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (runtime *WebRuntime) setAgentActive(response http.ResponseWriter, request *http.Request) {
	user, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	var input struct {
		Active bool `json:"active"`
	}
	if !decodeStrict(response, request, &input) {
		return
	}
	if err := runtime.Agents.SetActive(request.Context(), user.ID, request.PathValue("agent_id"), input.Active); err != nil {
		writeJSON(response, http.StatusForbidden, map[string]string{"error": "FORBIDDEN"})
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (runtime *WebRuntime) authorizeMutation(response http.ResponseWriter, request *http.Request) (identity.User, bool) {
	user, _, ok := runtime.authenticate(response, request)
	if !ok {
		return identity.User{}, false
	}
	if !runtime.validCSRF(request) {
		writeJSON(response, http.StatusForbidden, map[string]string{"error": "CSRF_REJECTED"})
		return identity.User{}, false
	}
	return user, true
}

func credentialDTO(credential agent.Credential) map[string]any {
	return map[string]any{
		"agent_id": credential.AgentID, "token": credential.Token,
		"expires_at": credential.ExpiresAt, "permanent_warning": credential.PermanentWarning,
	}
}
