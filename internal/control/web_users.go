package control

import (
	"context"
	"net/http"

	"github.com/fallingnight/akv/internal/identity"
)

type WebUserManager interface {
	ListUsers(context.Context, identity.User) ([]identity.User, error)
	UpdateUser(context.Context, identity.User, string, bool, bool) error
}

func (runtime *WebRuntime) listUsers(response http.ResponseWriter, request *http.Request) {
	actor, _, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	users, err := runtime.Users.ListUsers(request.Context(), actor)
	if err != nil {
		writeJSON(response, http.StatusForbidden, map[string]string{"error": "FORBIDDEN"})
		return
	}
	result := make([]map[string]any, 0, len(users))
	for _, user := range users {
		result = append(result, publicUserDTO(user))
	}
	writeJSON(response, http.StatusOK, result)
}

func (runtime *WebRuntime) updateUser(response http.ResponseWriter, request *http.Request) {
	actor, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	var input struct {
		Active     bool `json:"active"`
		ApproveAll bool `json:"approve_all"`
	}
	if !decodeStrict(response, request, &input) {
		return
	}
	if err := runtime.Users.UpdateUser(request.Context(), actor, request.PathValue("user_id"), input.Active, input.ApproveAll); err != nil {
		writeJSON(response, http.StatusForbidden, map[string]string{"error": "FORBIDDEN"})
		return
	}
	response.WriteHeader(http.StatusNoContent)
}
