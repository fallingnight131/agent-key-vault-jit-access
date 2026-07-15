package control

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealth(t *testing.T) {
	var logs bytes.Buffer
	server := NewServer(Config{ListenAddress: defaultListenAddress}, slog.New(slog.NewJSONHandler(&logs, nil)))
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	server.Handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := response.Body.String(); body != "{\"status\":\"ok\"}\n" {
		t.Fatalf("body = %q", body)
	}
	if !strings.Contains(logs.String(), `"path":"/healthz"`) {
		t.Fatalf("log = %q, want request path", logs.String())
	}
}
