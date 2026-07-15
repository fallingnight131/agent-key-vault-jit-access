package control

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/fallingnight/akv/internal/identity"
)

const (
	webSessionCookie = "akv_session"
	webCSRFCookie    = "akv_csrf"
	webSessionTTL    = 8 * time.Hour
)

type WebIdentity interface {
	Login(context.Context, string, string, time.Duration) (identity.Session, error)
	AuthenticateSession(context.Context, string) (identity.User, error)
	Logout(context.Context, string) error
}

type WebRuntime struct {
	Identity WebIdentity
	config   Config
}

func (runtime *WebRuntime) Register(mux *http.ServeMux, config Config) {
	runtime.config = config
	mux.HandleFunc("POST /v1/web/login", runtime.login)
	mux.HandleFunc("GET /v1/web/me", runtime.me)
	mux.HandleFunc("POST /v1/web/logout", runtime.logout)
}

func (runtime *WebRuntime) login(response http.ResponseWriter, request *http.Request) {
	if !runtime.sameOrigin(request) || !strings.HasPrefix(request.Header.Get("Content-Type"), "application/json") {
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

func (runtime *WebRuntime) setCookie(response http.ResponseWriter, name, value string, expires time.Time, httpOnly bool) {
	http.SetCookie(response, &http.Cookie{Name: name, Value: value, Path: "/", Expires: expires, HttpOnly: httpOnly, Secure: runtime.config.CookieSecure, SameSite: http.SameSiteStrictMode})
}

func (runtime *WebRuntime) clearCookie(response http.ResponseWriter, name string, httpOnly bool) {
	http.SetCookie(response, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: httpOnly, Secure: runtime.config.CookieSecure, SameSite: http.SameSiteStrictMode})
}

func publicUserDTO(user identity.User) map[string]any {
	return map[string]any{"id": user.ID, "username": user.Username, "is_admin": user.IsAdmin, "approve_all": user.ApproveAll}
}
