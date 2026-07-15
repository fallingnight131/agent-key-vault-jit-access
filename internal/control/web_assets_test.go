package control

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedWebConsoleSecurityHeaders(t *testing.T) {
	server := NewServer(Config{ListenAddress: "127.0.0.1:0"}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), nil, nil)
	for _, path := range []string{"/", "/assets/app.js", "/assets/app.css"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Security-Policy"), "object-src 'none'") {
			t.Fatalf("path=%s status=%d CSP=%q", path, response.Code, response.Header().Get("Content-Security-Policy"))
		}
	}
}

func TestWebConsoleDoesNotPersistOrUseUnsafeBusinessRendering(t *testing.T) {
	script, err := webFiles.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"localStorage", "sessionStorage", "innerHTML", "outerHTML", "console."} {
		if strings.Contains(string(script), forbidden) {
			t.Errorf("app.js contains forbidden browser primitive %q", forbidden)
		}
	}
}
