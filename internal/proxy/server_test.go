package proxy

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExecutionServerHealthIdentifiesProcessBoundary(t *testing.T) {
	server := NewRuntimeServer(ServerConfig{ListenAddress: defaultListenAddress}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK || response.Body.String() != "{\"service\":\"akv-execution-proxy\",\"status\":\"ok\"}\n" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}
