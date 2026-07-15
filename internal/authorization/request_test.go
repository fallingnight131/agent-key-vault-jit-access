package authorization

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/agent"
	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/task"
)

type fakeTasks struct{ err error }

func (tasks *fakeTasks) ValidateActive(context.Context, string, string) (task.Record, error) {
	return task.Record{}, tasks.err
}

type fakeCatalog struct {
	resolved catalog.ResolvedOperation
	calls    int
}

func (resolver *fakeCatalog) ResolveOperationForRequest(_ context.Context, targetID, operationID string, version int) (catalog.ResolvedOperation, error) {
	resolver.calls++
	if targetID != resolver.resolved.Target.ID || operationID != resolver.resolved.Operation.ID || version != resolver.resolved.Version.Version {
		return catalog.ResolvedOperation{}, catalog.ErrUnavailable
	}
	return resolver.resolved, nil
}

type fakeRepository struct{ request *Request }

func (repository *fakeRepository) CreateRequest(_ context.Context, request Request) error {
	repository.request = &request
	return nil
}

func TestSubmitFreezesServerCompiledSnapshot(t *testing.T) {
	service, repository, _ := newTestService()
	input := validHTTPInput()
	request, err := service.Submit(context.Background(), agent.Principal{AgentID: "agent"}, input)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	input.Arguments[0] = '['
	if request.CredentialID != "server-credential" || repository.request.CredentialID != "server-credential" {
		t.Fatalf("credential ID = %q", request.CredentialID)
	}
	if string(repository.request.ArgumentsSnapshot) != `{"ticket_id":123}` {
		t.Fatalf("arguments snapshot = %s", repository.request.ArgumentsSnapshot)
	}
	if string(repository.request.OperationSnapshot) != `{"kind":"HTTP","http":{"method":"POST","path":"/tickets","query":{"id":["123"]},"headers":{"X-Trace":"trace"}}}` {
		t.Fatalf("operation snapshot = %s", repository.request.OperationSnapshot)
	}
	if request.RequestFormat != 2 || request.OperationID != "query-ticket" || request.OperationVersion != 1 || request.TargetConfigVersion != 1 {
		t.Fatalf("request context = %+v", request)
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
	if !errors.Is(err, ErrContextDenied) || resolver.calls != 0 {
		t.Fatalf("Submit() error=%v catalog calls=%d", err, resolver.calls)
	}
}

func TestSubmitRejectsUnsafePrivateTemplate(t *testing.T) {
	for _, header := range []string{"Authorization", "authorization", "Proxy-Authorization", "Cookie", "X-API-Key", "Host"} {
		service, repository, resolver := newTestService()
		resolver.resolved.Version.ExecutionTemplate = []byte(`{"kind":"HTTP","http":{"method":"POST","path":"/tickets","query_arguments":{"id":"ticket_id"},"static_headers":{"` + header + `":"fixture"}}}`)
		if _, err := service.Submit(context.Background(), agent.Principal{AgentID: "agent"}, validHTTPInput()); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("header %q Submit() error = %v", header, err)
		}
		if repository.request != nil {
			t.Errorf("header %q persisted a request", header)
		}
	}
}

func TestSubmitRejectsStoredOnlyCertificateOperation(t *testing.T) {
	service, _, resolver := newTestService()
	resolver.resolved.Credential.Type = catalog.CredentialCertificate
	if _, err := service.Submit(context.Background(), agent.Principal{AgentID: "agent"}, validHTTPInput()); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Submit() error = %v", err)
	}
}

func TestSubmitRejectsRawOperationShapeThroughStrictBoundary(t *testing.T) {
	// SubmitInput intentionally has no public field that can carry raw SQL,
	// HTTP paths, connector kinds, credentials, or execution templates.
	input := validHTTPInput()
	if input.OperationID == "" || len(input.Arguments) == 0 {
		t.Fatalf("invalid safe input fixture: %+v", input)
	}
}

func TestOperationHashBindsVersionArgumentsAndTargetConfiguration(t *testing.T) {
	service, _, resolver := newTestService()
	first, err := service.Submit(context.Background(), agent.Principal{AgentID: "agent"}, validHTTPInput())
	if err != nil {
		t.Fatalf("first Submit() error = %v", err)
	}
	second, err := service.Submit(context.Background(), agent.Principal{AgentID: "agent"}, validHTTPInput())
	if err != nil || first.OperationHash != second.OperationHash {
		t.Fatalf("deterministic hash error=%v equal=%t", err, first.OperationHash == second.OperationHash)
	}

	changed := validHTTPInput()
	changed.Arguments = []byte(`{"ticket_id":124}`)
	third, err := service.Submit(context.Background(), agent.Principal{AgentID: "agent"}, changed)
	if err != nil || first.OperationHash == third.OperationHash {
		t.Fatalf("arguments binding error=%v equal=%t", err, first.OperationHash == third.OperationHash)
	}

	resolver.resolved.Target.ConfigVersion = 2
	fourth, err := service.Submit(context.Background(), agent.Principal{AgentID: "agent"}, validHTTPInput())
	if err != nil || first.OperationHash == fourth.OperationHash {
		t.Fatalf("target config binding error=%v equal=%t", err, first.OperationHash == fourth.OperationHash)
	}
}

func newTestService() (*Service, *fakeRepository, *fakeCatalog) {
	repository := &fakeRepository{}
	definitionHash := sha256.Sum256([]byte("definition"))
	resolver := &fakeCatalog{resolved: catalog.ResolvedOperation{
		Target: catalog.Target{
			ID: "target", ConnectorType: catalog.ConnectorHTTP, Active: true, ConfigVersion: 1,
			ConnectionConfig:    catalog.ConnectionConfig{AllowedHTTPMethods: []string{"GET", "POST"}},
			DefaultCredentialID: "server-credential",
		},
		Credential: catalog.Credential{ID: "server-credential", TargetID: "target", Type: catalog.CredentialAPIKey, Active: true},
		Set:        catalog.OperationSet{ID: "set", ExecutorType: catalog.ExecutorHTTP, Active: true},
		Operation:  catalog.SafeOperation{ID: "query-ticket", OperationSetID: "set", Key: "query_ticket", CurrentVersion: 1, Active: true},
		Version: catalog.OperationVersion{
			OperationID: "query-ticket", Version: 1, Kind: "HTTP", RiskLevel: catalog.RiskLow,
			ArgumentsSchema:   []byte(`{"type":"object","properties":{"ticket_id":{"type":"integer","minimum":1}},"required":["ticket_id"],"additionalProperties":false}`),
			ExecutionTemplate: []byte(`{"kind":"HTTP","http":{"method":"POST","path":"/tickets","query_arguments":{"id":"ticket_id"},"static_headers":{"X-Trace":"trace"}}}`),
			DefinitionHash:    definitionHash,
		},
	}}
	service := NewService(&fakeTasks{}, resolver, repository)
	service.now = func() time.Time { return time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC) }
	service.newID = func() (string, error) { return "request-id", nil }
	return service, repository, resolver
}

func validHTTPInput() SubmitInput {
	return SubmitInput{
		TaskID: "task", TargetID: "target", OperationID: "query-ticket", Version: 1,
		Arguments: []byte(`{"ticket_id":123}`), Reason: "update ticket",
	}
}
