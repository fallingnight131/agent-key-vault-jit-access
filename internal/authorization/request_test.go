package authorization

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/agent"
	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/task"
)

type fakeTasks struct {
	err error
}

func (tasks *fakeTasks) ValidateActive(context.Context, string, string) (task.Record, error) {
	return task.Record{}, tasks.err
}

type fakeCatalog struct {
	target     catalog.Target
	credential catalog.Credential
	calls      int
}

func (resolver *fakeCatalog) ResolveForRequest(context.Context, string) (catalog.Target, catalog.Credential, error) {
	resolver.calls++
	return resolver.target, resolver.credential, nil
}

type fakeRepository struct {
	request *Request
}

func (repository *fakeRepository) CreateRequest(_ context.Context, request Request) error {
	repository.request = &request
	return nil
}

func TestSubmitFreezesServerBoundSnapshot(t *testing.T) {
	service, repository, _ := newTestService()
	input := validHTTPInput()
	request, err := service.Submit(context.Background(), agent.Principal{AgentID: "agent"}, input)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	input.Operation.HTTP.Headers["X-Trace"] = "changed-after-submit"
	if request.CredentialID != "server-credential" || repository.request.CredentialID != "server-credential" {
		t.Fatalf("credential ID = %q", request.CredentialID)
	}
	if string(repository.request.OperationSnapshot) != `{"kind":"HTTP","http":{"method":"POST","path":"/tickets","headers":{"X-Trace":"trace"}}}` {
		t.Fatalf("snapshot = %s", repository.request.OperationSnapshot)
	}
	if request.ApprovalDeadline.Sub(request.CreatedAt) != ApprovalWait {
		t.Fatalf("approval wait = %v", request.ApprovalDeadline.Sub(request.CreatedAt))
	}
}

func TestSubmitValidatesTaskBeforeCatalog(t *testing.T) {
	tasks := &fakeTasks{err: errors.New("inactive fixture task")}
	resolver := &fakeCatalog{}
	service := NewService(tasks, resolver, &fakeRepository{})
	_, err := service.Submit(context.Background(), agent.Principal{AgentID: "agent"}, validHTTPInput())
	if !errors.Is(err, ErrContextDenied) {
		t.Fatalf("Submit() error = %v", err)
	}
	if resolver.calls != 0 {
		t.Fatalf("catalog calls = %d, want 0", resolver.calls)
	}
}

func TestSubmitRejectsAuthenticationHeaders(t *testing.T) {
	for _, header := range []string{"Authorization", "authorization", "Proxy-Authorization", "Cookie", "X-API-Key"} {
		service, repository, _ := newTestService()
		input := validHTTPInput()
		input.Operation.HTTP.Headers[header] = "fixture-value"
		if _, err := service.Submit(context.Background(), agent.Principal{AgentID: "agent"}, input); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("header %q Submit() error = %v", header, err)
		}
		if repository.request != nil {
			t.Errorf("header %q persisted a request", header)
		}
	}
}

func TestSubmitRejectsStoredOnlyCertificateOperation(t *testing.T) {
	service := NewService(
		&fakeTasks{},
		&fakeCatalog{target: catalog.Target{ID: "target", ConnectorType: catalog.ConnectorHTTP, ConnectionConfig: catalog.ConnectionConfig{AllowedHTTPMethods: []string{"POST"}}}, credential: catalog.Credential{ID: "credential", TargetID: "target", Type: catalog.CredentialCertificate, Active: true}},
		&fakeRepository{},
	)
	_, err := service.Submit(context.Background(), agent.Principal{AgentID: "agent"}, SubmitInput{
		TaskID: "task", TargetID: "target", Reason: "certificate must remain stored only",
		Operation: Operation{Kind: OperationHTTP, HTTP: &HTTPParameters{Method: "POST", Path: "/execute"}},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Submit() error = %v", err)
	}
}

func TestOperationHashBindsContextAndIsDeterministic(t *testing.T) {
	service, _, _ := newTestService()
	first, err := service.Submit(context.Background(), agent.Principal{AgentID: "agent"}, validHTTPInput())
	if err != nil {
		t.Fatalf("first Submit() error = %v", err)
	}
	second, err := service.Submit(context.Background(), agent.Principal{AgentID: "agent"}, validHTTPInput())
	if err != nil {
		t.Fatalf("second Submit() error = %v", err)
	}
	if first.OperationHash != second.OperationHash {
		t.Fatal("equal snapshots produced different hashes")
	}
	changed := validHTTPInput()
	changed.TaskID = "other-task"
	third, err := service.Submit(context.Background(), agent.Principal{AgentID: "agent"}, changed)
	if err != nil {
		t.Fatalf("third Submit() error = %v", err)
	}
	if first.OperationHash == third.OperationHash {
		t.Fatal("different task produced equal operation hash")
	}
}

func newTestService() (*Service, *fakeRepository, *fakeCatalog) {
	repository := &fakeRepository{}
	resolver := &fakeCatalog{
		target: catalog.Target{
			ID: "target", ConnectorType: catalog.ConnectorHTTP, Active: true,
			ConnectionConfig:    catalog.ConnectionConfig{AllowedHTTPMethods: []string{"GET", "POST"}},
			DefaultCredentialID: "server-credential",
		},
		credential: catalog.Credential{ID: "server-credential", TargetID: "target", Type: catalog.CredentialAPIKey, Active: true},
	}
	service := NewService(&fakeTasks{}, resolver, repository)
	service.now = func() time.Time { return time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC) }
	service.newID = func() (string, error) { return "request-id", nil }
	return service, repository, resolver
}

func validHTTPInput() SubmitInput {
	return SubmitInput{
		TaskID: "task", TargetID: "target", Reason: "update ticket",
		Operation: Operation{
			Kind: OperationHTTP,
			HTTP: &HTTPParameters{Method: "POST", Path: "/tickets", Headers: map[string]string{"X-Trace": "trace"}},
		},
	}
}
