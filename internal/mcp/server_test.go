package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/control"
	"github.com/fallingnight/akv/internal/domain"
)

type fakeAPI struct {
	mu         sync.Mutex
	heartbeats int
	ended      bool
}

func (*fakeAPI) ListTargets(context.Context) ([]Target, error) {
	return []Target{{ID: "target", Name: "tickets"}}, nil
}
func (*fakeAPI) BeginTask(context.Context) (Task, error) {
	return Task{TaskID: "task", Status: "ACTIVE", HeartbeatIntervalSeconds: 15}, nil
}
func (fake *fakeAPI) Heartbeat(context.Context, string) error {
	fake.mu.Lock()
	fake.heartbeats++
	fake.mu.Unlock()
	return nil
}
func (fake *fakeAPI) EndTask(context.Context, string, domain.TaskStatus) error {
	fake.mu.Lock()
	fake.ended = true
	fake.mu.Unlock()
	return nil
}
func (*fakeAPI) RequestAuthorization(context.Context, authorization.SubmitInput) (RequestResult, error) {
	return RequestResult{RequestID: "request"}, nil
}
func (*fakeAPI) Status(context.Context, string) (control.AuthorizationStatus, error) {
	return control.AuthorizationStatus{RequestID: "request", OperationKind: "HTTP"}, nil
}
func (*fakeAPI) Cancel(context.Context, string) error { return nil }
func (*fakeAPI) Execute(context.Context, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{"status":200}`), nil
}

func TestProtocolListsToolsWithoutTokenOrCredentialBypass(t *testing.T) {
	server := NewServer(&fakeAPI{})
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\"}\n")
	var output strings.Builder
	if err := server.Serve(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	body := output.String()
	for _, forbidden := range []string{"token_file", "agent_token", "credential_id", "base_url"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("tools/list exposes forbidden field %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, "execute_authorized_operation") || !strings.Contains(body, "2025-11-25") {
		t.Fatalf("protocol output=%s", body)
	}
}

func TestBeginStartsAndEndStopsBackgroundHeartbeat(t *testing.T) {
	api := &fakeAPI{}
	server := NewServer(api)
	server.heartbeatInterval = 5 * time.Millisecond
	if _, err := server.callTool(context.Background(), json.RawMessage(`{"name":"begin_task","arguments":{}}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(18 * time.Millisecond)
	api.mu.Lock()
	before := api.heartbeats
	api.mu.Unlock()
	if before < 2 {
		t.Fatalf("heartbeats=%d", before)
	}
	if _, err := server.callTool(context.Background(), json.RawMessage(`{"name":"end_task","arguments":{"task_id":"task","outcome":"COMPLETED"}}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(15 * time.Millisecond)
	api.mu.Lock()
	after, ended := api.heartbeats, api.ended
	api.mu.Unlock()
	if !ended || after != before {
		t.Fatalf("ended=%t heartbeats before=%d after=%d", ended, before, after)
	}
	server.Close()
}

func TestToolArgumentsRejectCredentialID(t *testing.T) {
	server := NewServer(&fakeAPI{})
	_, err := server.callTool(context.Background(), json.RawMessage(`{"name":"request_authorization","arguments":{"task_id":"task","target_id":"target","credential_id":"bypass","operation":{"kind":"HTTP","http":{"method":"GET","path":"/"}},"reason":"fixture"}}`))
	if err == nil {
		t.Fatal("credential_id bypass accepted")
	}
}
