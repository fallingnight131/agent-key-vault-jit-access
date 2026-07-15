package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/control"
	"github.com/fallingnight/akv/internal/domain"
	"github.com/fallingnight/akv/internal/task"
)

type API interface {
	ListTargets(context.Context) ([]Target, error)
	BeginTask(context.Context) (Task, error)
	Heartbeat(context.Context, string) error
	EndTask(context.Context, string, domain.TaskStatus) error
	RequestAuthorization(context.Context, authorization.SubmitInput) (RequestResult, error)
	Status(context.Context, string) (control.AuthorizationStatus, error)
	Cancel(context.Context, string) error
	Execute(context.Context, string, string) (json.RawMessage, error)
}
type Server struct {
	api               API
	heartbeatInterval time.Duration
	mu                sync.Mutex
	heartbeats        map[string]context.CancelFunc
}

func NewServer(api API) *Server {
	return &Server{api: api, heartbeatInterval: task.HeartbeatInterval, heartbeats: make(map[string]context.CancelFunc)}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func (server *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		var request rpcRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			_ = encoder.Encode(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "Parse error"}})
			continue
		}
		response, respond := server.handle(ctx, request)
		if respond {
			if err := encoder.Encode(response); err != nil {
				server.Close()
				return err
			}
		}
	}
	server.Close()
	return scanner.Err()
}
func (server *Server) Close() {
	server.mu.Lock()
	defer server.mu.Unlock()
	for id, cancel := range server.heartbeats {
		cancel()
		delete(server.heartbeats, id)
	}
}

func (server *Server) handle(ctx context.Context, request rpcRequest) (rpcResponse, bool) {
	base := rpcResponse{JSONRPC: "2.0", ID: request.ID}
	if request.JSONRPC != "2.0" {
		base.Error = &rpcError{Code: -32600, Message: "Invalid Request"}
		return base, true
	}
	switch request.Method {
	case "notifications/initialized":
		return base, false
	case "initialize":
		protocolVersion := "2025-11-25"
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if len(request.Params) > 0 && json.Unmarshal(request.Params, &params) == nil && params.ProtocolVersion == "2025-06-18" {
			protocolVersion = params.ProtocolVersion
		}
		base.Result = map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]string{"name": "akv-mcp-server", "version": "0.1.0"}}
		return base, true
	case "tools/list":
		base.Result = map[string]any{"tools": toolDefinitions()}
		return base, true
	case "tools/call":
		result, err := server.callTool(ctx, request.Params)
		if err != nil {
			base.Result = toolResult(map[string]string{"error": "AKV_TOOL_FAILED"}, true)
		} else {
			base.Result = result
		}
		return base, true
	default:
		base.Error = &rpcError{Code: -32601, Message: "Method not found"}
		return base, true
	}
}

func (server *Server) callTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := decode(raw, &call); err != nil {
		return nil, err
	}
	var result any
	switch call.Name {
	case "list_targets":
		if err := decode(call.Arguments, &struct{}{}); err != nil {
			return nil, err
		}
		value, err := server.api.ListTargets(ctx)
		result = value
		if err != nil {
			return nil, err
		}
	case "get_target":
		var input struct {
			TargetID string `json:"target_id"`
		}
		if err := decode(call.Arguments, &input); err != nil {
			return nil, err
		}
		targets, err := server.api.ListTargets(ctx)
		if err != nil {
			return nil, err
		}
		found := false
		for _, target := range targets {
			if target.ID == input.TargetID {
				result = target
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("not found")
		}
	case "begin_task":
		if err := decode(call.Arguments, &struct{}{}); err != nil {
			return nil, err
		}
		task, err := server.api.BeginTask(ctx)
		if err != nil {
			return nil, err
		}
		server.startHeartbeat(task.TaskID)
		result = task
	case "heartbeat_task":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := decode(call.Arguments, &input); err != nil {
			return nil, err
		}
		if err := server.api.Heartbeat(ctx, input.TaskID); err != nil {
			return nil, err
		}
		result = map[string]string{"status": "ACTIVE"}
	case "end_task":
		var input struct {
			TaskID  string            `json:"task_id"`
			Outcome domain.TaskStatus `json:"outcome"`
		}
		if err := decode(call.Arguments, &input); err != nil {
			return nil, err
		}
		if err := server.api.EndTask(ctx, input.TaskID, input.Outcome); err != nil {
			return nil, err
		}
		server.stopHeartbeat(input.TaskID)
		result = map[string]string{"status": string(input.Outcome)}
	case "request_authorization":
		var input authorization.SubmitInput
		if err := decode(call.Arguments, &input); err != nil {
			return nil, err
		}
		value, err := server.api.RequestAuthorization(ctx, input)
		result = value
		if err != nil {
			return nil, err
		}
	case "get_authorization_status":
		var input struct {
			RequestID string `json:"request_id"`
		}
		if err := decode(call.Arguments, &input); err != nil {
			return nil, err
		}
		value, err := server.api.Status(ctx, input.RequestID)
		result = value
		if err != nil {
			return nil, err
		}
	case "cancel_authorization_request":
		var input struct {
			RequestID string `json:"request_id"`
		}
		if err := decode(call.Arguments, &input); err != nil {
			return nil, err
		}
		if err := server.api.Cancel(ctx, input.RequestID); err != nil {
			return nil, err
		}
		result = map[string]string{"status": "REVOCATION_REQUESTED"}
	case "execute_authorized_operation":
		var input struct {
			RequestID string `json:"request_id"`
			TaskID    string `json:"task_id"`
		}
		if err := decode(call.Arguments, &input); err != nil {
			return nil, err
		}
		value, err := server.api.Execute(ctx, input.RequestID, input.TaskID)
		if err != nil {
			return nil, err
		}
		result = json.RawMessage(value)
	default:
		return nil, errors.New("unknown tool")
	}
	return toolResult(result, false), nil
}

func (server *Server) startHeartbeat(taskID string) {
	server.stopHeartbeat(taskID)
	ctx, cancel := context.WithCancel(context.Background())
	server.mu.Lock()
	server.heartbeats[taskID] = cancel
	server.mu.Unlock()
	go func() {
		ticker := time.NewTicker(server.heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				heartbeatCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				err := server.api.Heartbeat(heartbeatCtx, taskID)
				stop()
				if err != nil {
					server.stopHeartbeat(taskID)
					return
				}
			}
		}
	}()
}
func (server *Server) stopHeartbeat(taskID string) {
	server.mu.Lock()
	cancel := server.heartbeats[taskID]
	delete(server.heartbeats, taskID)
	server.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func decode(raw []byte, destination any) error {
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("invalid input")
	}
	return nil
}
func toolResult(value any, isError bool) map[string]any {
	encoded, _ := json.Marshal(value)
	return map[string]any{"content": []map[string]string{{"type": "text", "text": string(encoded)}}, "structuredContent": value, "isError": isError}
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}
func stringField(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}
func toolDefinitions() []tool {
	empty := objectSchema(map[string]any{})
	id := func(name string) map[string]any { return objectSchema(map[string]any{name: stringField(name)}, name) }
	httpOperation := objectSchema(map[string]any{
		"kind": map[string]any{"const": "HTTP"},
		"http": objectSchema(map[string]any{
			"method": map[string]any{"type": "string", "enum": []string{"GET", "POST", "PUT", "PATCH", "DELETE"}},
			"path":   stringField("Relative target path beginning with /"),
			"query":  map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "array", "items": map[string]string{"type": "string"}}},
			"body":   map[string]any{"type": "string", "description": "Optional base64 request body"},
		}, "method", "path"),
	}, "kind", "http")
	postgresOperation := objectSchema(map[string]any{
		"kind":       map[string]any{"type": "string", "enum": []string{"POSTGRESQL_STATEMENT", "POSTGRESQL_TRANSACTION"}},
		"postgresql": objectSchema(map[string]any{"statements": map[string]any{"type": "array", "minItems": 1, "items": objectSchema(map[string]any{"sql": stringField("Parameterized SQL"), "arguments": map[string]any{"type": "array"}}, "sql")}}, "statements"),
	}, "kind", "postgresql")
	signOperation := objectSchema(map[string]any{
		"kind": map[string]any{"const": "SIGN"}, "sign": objectSchema(map[string]any{"digest_algorithm": map[string]any{"type": "string", "enum": []string{"sha2-256", "sha2-384", "sha2-512"}}, "digest": map[string]any{"type": "string", "description": "Base64 digest bytes"}}, "digest_algorithm", "digest"),
	}, "kind", "sign")
	operationSchema := map[string]any{"oneOf": []any{httpOperation, postgresOperation, signOperation}, "description": "Frozen typed operation; URLs, credentials, and authentication headers are not accepted"}
	return []tool{
		{Name: "list_targets", Description: "List registered target systems available for authorization.", InputSchema: empty}, {Name: "get_target", Description: "Get the safe metadata and risk context for one registered target.", InputSchema: id("target_id")}, {Name: "begin_task", Description: "Begin a server-bound AKV task and start automatic heartbeats.", InputSchema: empty}, {Name: "heartbeat_task", Description: "Send an explicit task heartbeat; automatic heartbeats continue in the background.", InputSchema: id("task_id")},
		{Name: "end_task", Description: "End a task and revoke unfinished grants.", InputSchema: objectSchema(map[string]any{"task_id": stringField("Server task ID"), "outcome": map[string]any{"type": "string", "enum": []string{"COMPLETED", "FAILED", "CANCELLED", "TIMED_OUT"}}}, "task_id", "outcome")},
		{Name: "request_authorization", Description: "Request one human-approved operation on one registered target.", InputSchema: objectSchema(map[string]any{"task_id": stringField("Server task ID"), "target_id": stringField("Registered target ID"), "operation": operationSchema, "reason": stringField("Human-readable reason")}, "task_id", "target_id", "operation", "reason")},
		{Name: "get_authorization_status", Description: "Get approval, grant, execution, and reclaim status.", InputSchema: id("request_id")}, {Name: "cancel_authorization_request", Description: "Revoke an approved or executing authorization request owned by this Agent.", InputSchema: id("request_id")}, {Name: "execute_authorized_operation", Description: "Execute an approved operation once through the protected proxy.", InputSchema: objectSchema(map[string]any{"request_id": stringField("Authorization request ID"), "task_id": stringField("Bound task ID")}, "request_id", "task_id")},
	}
}
