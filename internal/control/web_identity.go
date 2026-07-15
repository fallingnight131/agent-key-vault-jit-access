package control

import (
	"context"
	"crypto/subtle"
	"errors"
	"mime"
	"net/http"
	"time"

	"github.com/fallingnight/akv/internal/identity"
)

const (
	webSessionCookie = "akv_session"
	webCSRFCookie    = "akv_csrf"
	webSessionTTL    = 8 * time.Hour
)

type WebIdentity interface {
	Register(context.Context, string, string, time.Duration) (identity.Session, error)
	Login(context.Context, string, string, time.Duration) (identity.Session, error)
	AuthenticateSession(context.Context, string) (identity.User, error)
	Logout(context.Context, string) error
}

type WebRuntime struct {
	Identity       WebIdentity
	Agents         WebAgentManager
	Users          WebUserManager
	Catalog        WebCatalogManager
	ApprovalReader WebApprovalReader
	Approvals      WebApprovalDecider
	Revocations    WebRevoker
	config         Config
}

func (runtime *WebRuntime) Register(mux *http.ServeMux, config Config) {
	runtime.config = config
	mux.HandleFunc("POST /v1/web/register", runtime.register)
	mux.HandleFunc("POST /v1/web/login", runtime.login)
	mux.HandleFunc("GET /v1/web/me", runtime.me)
	mux.HandleFunc("POST /v1/web/logout", runtime.logout)
	if runtime.Agents != nil {
		mux.HandleFunc("GET /v1/web/agents", runtime.listAgents)
		mux.HandleFunc("POST /v1/web/agents", runtime.registerAgent)
		mux.HandleFunc("POST /v1/web/agents/{agent_id}/rotate-token", runtime.rotateAgentToken)
		mux.HandleFunc("DELETE /v1/web/agents/{agent_id}/token", runtime.revokeAgentToken)
		mux.HandleFunc("PATCH /v1/web/agents/{agent_id}", runtime.setAgentActive)
	}
	if runtime.Users != nil {
		mux.HandleFunc("GET /v1/web/users", runtime.listUsers)
		mux.HandleFunc("PATCH /v1/web/users/{user_id}", runtime.updateUser)
	}
	if runtime.Catalog != nil {
		mux.HandleFunc("GET /v1/web/catalog", runtime.listCatalog)
		mux.HandleFunc("POST /v1/web/targets", runtime.createTarget)
		mux.HandleFunc("PATCH /v1/web/targets/{target_id}", runtime.updateTarget)
		mux.HandleFunc("PATCH /v1/web/credentials/{credential_id}", runtime.updateCredential)
		mux.HandleFunc("POST /v1/web/operation-sets", runtime.createOperationSet)
		mux.HandleFunc("PATCH /v1/web/operation-sets/{set_id}", runtime.setOperationSetActive)
		mux.HandleFunc("POST /v1/web/operation-sets/{set_id}/operations", runtime.createOperation)
		mux.HandleFunc("POST /v1/web/operations/{operation_id}/versions", runtime.publishOperationVersion)
		mux.HandleFunc("PATCH /v1/web/operations/{operation_id}", runtime.setOperationActive)
		mux.HandleFunc("PUT /v1/web/targets/{target_id}/operations/{operation_id}", runtime.bindOperation)
	}
	if runtime.ApprovalReader != nil && runtime.Approvals != nil && runtime.Revocations != nil {
		mux.HandleFunc("GET /v1/web/authorizations", runtime.listAuthorizations)
		mux.HandleFunc("POST /v1/web/authorizations/{request_id}/decision", runtime.decideAuthorization)
		mux.HandleFunc("POST /v1/web/authorizations/{request_id}/revoke", runtime.revokeAuthorization)
		mux.HandleFunc("GET /v1/web/authorizations/{request_id}/audit", runtime.authorizationAudit)
		mux.HandleFunc("GET /v1/web/audit", runtime.listAuditEvents)
		mux.HandleFunc("GET /v1/web/incidents", runtime.listIncidents)
		mux.HandleFunc("POST /v1/web/incidents/{incident_id}/resolve", runtime.resolveIncident)
	}
}

func (runtime *WebRuntime) register(response http.ResponseWriter, request *http.Request) {
	if !runtime.sameOrigin(request) || !hasJSONContentType(request) {
		writeJSON(response, http.StatusForbidden, map[string]string{"error": "FORBIDDEN"})
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeStrict(response, request, &input) {
		return
	}
	session, err := runtime.Identity.Register(request.Context(), input.Username, input.Password, webSessionTTL)
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrInvalidInput):
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_REGISTRATION"})
		case errors.Is(err, identity.ErrUsernameUnavailable):
			writeJSON(response, http.StatusConflict, map[string]string{"error": "USERNAME_UNAVAILABLE"})
		case errors.Is(err, identity.ErrRegistrationUnavailable):
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "REGISTRATION_UNAVAILABLE"})
		default:
			writeJSON(response, http.StatusInternalServerError, map[string]string{"error": "INTERNAL"})
		}
		return
	}
	runtime.setCookie(response, webSessionCookie, session.Token, session.ExpiresAt, true)
	runtime.setCookie(response, webCSRFCookie, session.CSRFToken, session.ExpiresAt, false)
	writeJSON(response, http.StatusCreated, publicUserDTO(session.User))
}

func (runtime *WebRuntime) login(response http.ResponseWriter, request *http.Request) {
	if !runtime.sameOrigin(request) || !hasJSONContentType(request) {
		writeJSON(response, http.StatusForbidden, map[string]string{"error": "FORBIDDEN"})
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeStrict(response, request, &input) {
		return
	}
	session, err := runtime.Identity.Login(request.Context(), input.Username, input.Password, webSessionTTL)
	if err != nil {
		writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "INVALID_CREDENTIALS"})
		return
	}
	runtime.setCookie(response, webSessionCookie, session.Token, session.ExpiresAt, true)
	runtime.setCookie(response, webCSRFCookie, session.CSRFToken, session.ExpiresAt, false)
	writeJSON(response, http.StatusOK, publicUserDTO(session.User))
}

func (runtime *WebRuntime) me(response http.ResponseWriter, request *http.Request) {
	user, _, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	writeJSON(response, http.StatusOK, publicUserDTO(user))
}

func (runtime *WebRuntime) logout(response http.ResponseWriter, request *http.Request) {
	_, token, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	if !runtime.validCSRF(request) {
		writeJSON(response, http.StatusForbidden, map[string]string{"error": "CSRF_REJECTED"})
		return
	}
	if err := runtime.Identity.Logout(request.Context(), token); err != nil {
		writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "UNAUTHORIZED"})
		return
	}
	runtime.clearCookie(response, webSessionCookie, true)
	runtime.clearCookie(response, webCSRFCookie, false)
	response.WriteHeader(http.StatusNoContent)
}

func (runtime *WebRuntime) authenticate(response http.ResponseWriter, request *http.Request) (identity.User, string, bool) {
	cookie, err := request.Cookie(webSessionCookie)
	if err != nil || cookie.Value == "" {
		writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "UNAUTHORIZED"})
		return identity.User{}, "", false
	}
	user, err := runtime.Identity.AuthenticateSession(request.Context(), cookie.Value)
	if err != nil {
		writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "UNAUTHORIZED"})
		return identity.User{}, "", false
	}
	return user, cookie.Value, true
}

func (runtime *WebRuntime) validCSRF(request *http.Request) bool {
	if !runtime.sameOrigin(request) {
		return false
	}
	cookie, err := request.Cookie(webCSRFCookie)
	header := request.Header.Get("X-AKV-CSRF")
	return err == nil && cookie.Value != "" && len(cookie.Value) == len(header) && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) == 1
}

func (runtime *WebRuntime) sameOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	return origin == "" || origin == runtime.config.PublicOrigin
}

func hasJSONContentType(request *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

func (runtime *WebRuntime) setCookie(response http.ResponseWriter, name, value string, expires time.Time, httpOnly bool) {
	http.SetCookie(response, &http.Cookie{Name: name, Value: value, Path: "/", Expires: expires, HttpOnly: httpOnly, Secure: runtime.config.CookieSecure, SameSite: http.SameSiteStrictMode})
}

func (runtime *WebRuntime) clearCookie(response http.ResponseWriter, name string, httpOnly bool) {
	http.SetCookie(response, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: httpOnly, Secure: runtime.config.CookieSecure, SameSite: http.SameSiteStrictMode})
}

func publicUserDTO(user identity.User) map[string]any {
	return map[string]any{"id": user.ID, "username": user.Username, "is_admin": user.IsAdmin, "approve_all": user.ApproveAll, "owner_active": user.OwnerActive}
}
