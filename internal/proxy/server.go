package proxy

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"
)

const defaultListenAddress = "127.0.0.1:8081"

type ServerConfig struct {
	ListenAddress string
}

func ServerConfigFromEnv() (ServerConfig, error) {
	address := os.Getenv("AKV_EXECUTION_LISTEN_ADDRESS")
	if address == "" {
		address = defaultListenAddress
	}
	if _, err := net.ResolveTCPAddr("tcp", address); err != nil {
		return ServerConfig{}, fmt.Errorf("AKV_EXECUTION_LISTEN_ADDRESS: %w", err)
	}
	return ServerConfig{ListenAddress: address}, nil
}

// NewRuntimeServer creates only the execution-plane HTTP boundary. Control
// plane Vault write capabilities are intentionally absent from its inputs.
func NewRuntimeServer(config ServerConfig, logger *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]string{"service": "akv-execution-proxy", "status": "ok"})
	})
	return &http.Server{
		Addr: config.ListenAddress, Handler: executionRequestLogger(logger, mux),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
	}
}

func executionRequestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		next.ServeHTTP(response, request)
		logger.Info("execution http request", "method", request.Method, "path", request.URL.Path,
			"duration_ms", time.Since(startedAt).Milliseconds())
	})
}
