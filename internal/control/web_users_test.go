package control

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fallingnight/akv/internal/identity"
)

type webUsersFake struct {
	updated bool
}

func (fake *webUsersFake) ListUsers(_ context.Context, actor identity.User) ([]identity.User, error) {
	if !actor.IsAdmin {
		return nil, identity.ErrInvalidInput
	}
	return []identity.User{{ID: "existing", Username: "existing-user", ApproveAll: true, OwnerActive: true}}, nil
}

func (fake *webUsersFake) UpdateUser(_ context.Context, actor identity.User, userID string, active, approveAll bool) error {
	if !actor.IsAdmin || userID != "existing" || active || !approveAll {
		return identity.ErrInvalidInput
	}
	fake.updated = true
	return nil
}

func TestAdminWebUserManagement(t *testing.T) {
	users := &webUsersFake{}
	config := Config{ListenAddress: "127.0.0.1:0", PublicOrigin: "https://akv.example.test"}
	server := NewServer(config, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), nil, &WebRuntime{Identity: &webIdentityFake{}, Users: users})

	request := authenticatedWebRequest(http.MethodGet, "/v1/web/users", "")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%q", response.Code, response.Body.String())
	}

	request = authenticatedWebRequest(http.MethodPatch, "/v1/web/users/existing", `{"active":false,"approve_all":true}`)
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || !users.updated {
		t.Fatalf("update status=%d updated=%t body=%q", response.Code, users.updated, response.Body.String())
	}
}
