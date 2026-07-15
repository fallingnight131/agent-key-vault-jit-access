package control

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/identity"
)

type webIdentityFake struct {
	revoked bool
}

func (fake *webIdentityFake) Login(_ context.Context, username, password string, _ time.Duration) (identity.Session, error) {
	if username != "admin" || password != "password" {
		return identity.Session{}, identity.ErrInvalidCredentials
	}
	return identity.Session{
		Token: "session-secret", CSRFToken: "csrf-token", ExpiresAt: time.Now().Add(time.Hour),
		User: identity.User{ID: "user", Username: "admin", IsAdmin: true, OwnerActive: true},
	}, nil
}

func (fake *webIdentityFake) AuthenticateSession(_ context.Context, token string) (identity.User, error) {
	if token != "session-secret" || fake.revoked {
		return identity.User{}, identity.ErrInvalidCredentials
	}
	return identity.User{ID: "user", Username: "admin", IsAdmin: true, OwnerActive: true}, nil
}

func (fake *webIdentityFake) Logout(_ context.Context, token string) error {
	if token != "session-secret" || fake.revoked {
		return identity.ErrInvalidCredentials
	}
	fake.revoked = true
	return nil
}

func TestWebLoginUsesProtectedCookiesAndNoTokenBody(t *testing.T) {
	server := testWebServer(&webIdentityFake{}, true)
	request := httptest.NewRequest(http.MethodPost, "/v1/web/login", strings.NewReader(`{"username":"admin","password":"password"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://akv.example.test")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "session-secret") || strings.Contains(response.Body.String(), "csrf-token") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 2 || cookies[0].Name != webSessionCookie || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode || cookies[1].HttpOnly {
		t.Fatalf("cookies = %+v", cookies)
	}
}

func TestWebLogoutRequiresCSRFAndRevokesSession(t *testing.T) {
	identityFake := &webIdentityFake{}
	server := testWebServer(identityFake, false)
	request := httptest.NewRequest(http.MethodPost, "/v1/web/logout", nil)
	request.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session-secret"})
	request.AddCookie(&http.Cookie{Name: webCSRFCookie, Value: "csrf-token"})
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || identityFake.revoked {
		t.Fatalf("missing CSRF status=%d revoked=%t", response.Code, identityFake.revoked)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/web/logout", nil)
	request.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session-secret"})
	request.AddCookie(&http.Cookie{Name: webCSRFCookie, Value: "csrf-token"})
	request.Header.Set("X-AKV-CSRF", "csrf-token")
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || !identityFake.revoked {
		t.Fatalf("valid CSRF status=%d revoked=%t body=%q", response.Code, identityFake.revoked, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/web/me", nil)
	request.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session-secret"})
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session status=%d", response.Code)
	}
}

func TestWebLoginRejectsCrossOriginAndUnknownFields(t *testing.T) {
	server := testWebServer(&webIdentityFake{}, false)
	for name, fixture := range map[string][2]string{
		"cross origin":  {"https://evil.example", `{"username":"admin","password":"password"}`},
		"unknown field": {"https://akv.example.test", `{"username":"admin","password":"password","token":"attacker"}`},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/web/login", strings.NewReader(fixture[1]))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", fixture[0])
			response := httptest.NewRecorder()
			server.Handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden && response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
}

func testWebServer(identityService WebIdentity, secure bool) *http.Server {
	if identityService == nil {
		identityService = &failingWebIdentity{}
	}
	config := Config{ListenAddress: "127.0.0.1:0", PublicOrigin: "https://akv.example.test", CookieSecure: secure}
	return NewServer(config, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), nil, &WebRuntime{Identity: identityService})
}

type failingWebIdentity struct{}

func (*failingWebIdentity) Login(context.Context, string, string, time.Duration) (identity.Session, error) {
	return identity.Session{}, errors.New("unavailable")
}
func (*failingWebIdentity) AuthenticateSession(context.Context, string) (identity.User, error) {
	return identity.User{}, errors.New("unavailable")
}
func (*failingWebIdentity) Logout(context.Context, string) error { return errors.New("unavailable") }
