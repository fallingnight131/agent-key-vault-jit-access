package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fallingnight/akv/internal/agent"
	"github.com/fallingnight/akv/internal/authorization"
)

const defaultListenAddress = "127.0.0.1:8081"

type ServerConfig struct {
	ListenAddress    string
	DatabaseDSNFile  string
	OpenBaoAddress   string
	OpenBaoTokenFile string
}

func ServerConfigFromEnv() (ServerConfig, error) {
	address := os.Getenv("AKV_EXECUTION_LISTEN_ADDRESS")
	if address == "" {
		address = defaultListenAddress
	}
	if _, err := net.ResolveTCPAddr("tcp", address); err != nil {
		return ServerConfig{}, fmt.Errorf("AKV_EXECUTION_LISTEN_ADDRESS: %w", err)
	}
	config := ServerConfig{
		ListenAddress:    address,
		DatabaseDSNFile:  os.Getenv("AKV_DATABASE_DSN_FILE"),
		OpenBaoAddress:   os.Getenv("AKV_OPENBAO_ADDRESS"),
		OpenBaoTokenFile: os.Getenv("AKV_OPENBAO_TOKEN_FILE"),
	}
	if config.DatabaseDSNFile == "" || config.OpenBaoAddress == "" || config.OpenBaoTokenFile == "" {
		return ServerConfig{}, errors.New("AKV_DATABASE_DSN_FILE, AKV_OPENBAO_ADDRESS, and AKV_OPENBAO_TOKEN_FILE are required")
	}
	return config, nil
}

type AgentAuthenticator interface {
	Authenticate(context.Context, string) (agent.Principal, error)
}

type Runtime struct {
	Authenticator AgentAuthenticator
	Plans         PlanStore
	HTTP          HTTPExecutor
	PostgreSQL    PostgreSQLExecutor
	Sign          SignExecutor
}

type HTTPExecutor interface {
	Execute(context.Context, string, string, string) (HTTPResult, error)
}
type PostgreSQLExecutor interface {
	Execute(context.Context, string, string, string) (PostgreSQLResult, error)
}
type SignExecutor interface {
	Execute(context.Context, string, string, string) ([]byte, error)
}

// NewRuntimeServer creates only the execution-plane HTTP boundary. Control
// plane Vault write capabilities are intentionally absent from its inputs.
func NewRuntimeServer(config ServerConfig, logger *slog.Logger, runtime *Runtime) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]string{"service": "akv-execution-proxy", "status": "ok"})
	})
	if runtime != nil {
		mux.HandleFunc("POST /v1/execute", runtime.execute)
	}
	return &http.Server{
		Addr: config.ListenAddress, Handler: executionRequestLogger(logger, mux),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
	}
}

type executeRequest struct {
	RequestID string `json:"request_id"`
	TaskID    string `json:"task_id"`
}

func (runtime *Runtime) authenticateAndDecode(response http.ResponseWriter, request *http.Request) (agent.Principal, executeRequest, bool) {
	authorization := request.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") || len(authorization) <= len("Bearer ") {
		writePublicJSON(response, http.StatusUnauthorized, map[string]string{"error": "UNAUTHORIZED"})
		return agent.Principal{}, executeRequest{}, false
	}
	principal, err := runtime.Authenticator.Authenticate(request.Context(), strings.TrimPrefix(authorization, "Bearer "))
	request.Header.Del("Authorization")
	if err != nil {
		writePublicJSON(response, http.StatusUnauthorized, map[string]string{"error": "UNAUTHORIZED"})
		return agent.Principal{}, executeRequest{}, false
	}
	var input executeRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.RequestID == "" || input.TaskID == "" {
		writePublicJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_REQUEST"})
		return agent.Principal{}, executeRequest{}, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writePublicJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_REQUEST"})
		return agent.Principal{}, executeRequest{}, false
	}
	return principal, input, true
}

func (runtime *Runtime) execute(response http.ResponseWriter, request *http.Request) {
	principal, input, ok := runtime.authenticateAndDecode(response, request)
	if !ok {
		return
	}
	if runtime.Plans == nil {
		writeExecutionError(response, ErrExecutionDenied)
		return
	}
	plan, err := runtime.Plans.LoadPlan(request.Context(), input.RequestID)
	if err != nil || plan.AgentID != principal.AgentID || plan.TaskID != input.TaskID {
		writeExecutionError(response, ErrExecutionDenied)
		return
	}
	switch plan.Operation.Kind {
	case authorization.OperationHTTP:
		if runtime.HTTP == nil {
			writeExecutionError(response, ErrExecutionDenied)
			return
		}
		result, err := runtime.HTTP.Execute(request.Context(), input.RequestID, principal.AgentID, input.TaskID)
		if err != nil {
			writeExecutionError(response, err)
			return
		}
		writePublicJSON(response, http.StatusOK, map[string]any{"operation_kind": plan.Operation.Kind, "result": result})
	case authorization.OperationPostgreSQLStatement, authorization.OperationPostgreSQLBatch:
		if runtime.PostgreSQL == nil {
			writeExecutionError(response, ErrExecutionDenied)
			return
		}
		result, err := runtime.PostgreSQL.Execute(request.Context(), input.RequestID, principal.AgentID, input.TaskID)
		if err != nil {
			writeExecutionError(response, err)
			return
		}
		writePublicJSON(response, http.StatusOK, map[string]any{"operation_kind": plan.Operation.Kind, "result": result})
	case authorization.OperationSign:
		if runtime.Sign == nil {
			writeExecutionError(response, ErrExecutionDenied)
			return
		}
		result, err := runtime.Sign.Execute(request.Context(), input.RequestID, principal.AgentID, input.TaskID)
		if err != nil {
			writeExecutionError(response, err)
			return
		}
		writePublicJSON(response, http.StatusOK, map[string]any{"operation_kind": plan.Operation.Kind, "result": map[string][]byte{"signature": result}})
	default:
		writeExecutionError(response, ErrExecutionDenied)
	}
}

func writeExecutionError(response http.ResponseWriter, err error) {
	code := "EXECUTION_DENIED"
	var publicError *PublicError
	if errors.As(err, &publicError) {
		code = publicError.Code
	}
	writePublicJSON(response, http.StatusForbidden, map[string]string{"error": code})
}

func writePublicJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func ReadProtectedConfigFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat protected config file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("protected config file must be inaccessible to group and others")
	}
	value, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read protected config file: %w", err)
	}
	value = []byte(strings.TrimSpace(string(value)))
	if len(value) == 0 {
		return nil, errors.New("protected config file is empty")
	}
	return value, nil
}

func executionRequestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		next.ServeHTTP(response, request)
		logger.Info("execution http request", "method", request.Method, "path", request.URL.Path,
			"duration_ms", time.Since(startedAt).Milliseconds())
	})
}
