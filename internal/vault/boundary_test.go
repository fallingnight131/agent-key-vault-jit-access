package vault

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type fakeExecutionClient struct {
	readCalls   int
	issueCalls  int
	revokeCalls int
	issueError  error
	lastLease   string
}

func (client *fakeExecutionClient) ReadKV(context.Context, string, *int) (map[string]*SensitiveValue, error) {
	client.readCalls++
	return map[string]*SensitiveValue{"token": NewSensitiveValue([]byte("fixture-secret"))}, nil
}

func (client *fakeExecutionClient) Sign(context.Context, string, string, []byte) ([]byte, error) {
	return []byte("fixture-signature"), nil
}

func (client *fakeExecutionClient) IssueDatabase(context.Context, string, time.Duration) (DynamicCredential, error) {
	client.issueCalls++
	if client.issueError != nil {
		return DynamicCredential{}, client.issueError
	}
	return DynamicCredential{
		Username: NewSensitiveValue([]byte("temporary-user")),
		Password: NewSensitiveValue([]byte("temporary-password")), LeaseID: "lease-id",
	}, nil
}

func (client *fakeExecutionClient) RevokeLease(_ context.Context, leaseID string) error {
	client.revokeCalls++
	client.lastLease = leaseID
	return nil
}

func TestSensitiveValueRedactsFormattingAndDestroys(t *testing.T) {
	value := NewSensitiveValue([]byte("never-print-this"))
	if formatted := fmt.Sprintf("%s %#v", value, value); strings.Contains(formatted, "never-print-this") {
		t.Fatalf("formatted sensitive value leaked: %q", formatted)
	}
	value.Destroy()
	if err := value.WithBytes(func([]byte) error { return nil }); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("WithBytes() after Destroy error = %v", err)
	}
}

func TestDynamicIssueFailureNeverFallsBackToStatic(t *testing.T) {
	client := &fakeExecutionClient{issueError: errors.New("fixture issue failure")}
	_, err := Acquire(context.Background(), client, Reference{
		Kind: ReferencePostgreSQLDynamic, Path: "database/creds/app", TTL: time.Minute,
	})
	if err == nil {
		t.Fatal("Acquire() error = nil")
	}
	if client.issueCalls != 1 || client.readCalls != 0 {
		t.Fatalf("issue calls = %d, static read calls = %d", client.issueCalls, client.readCalls)
	}
}

func TestStaticHandleDestroysMemoryWithoutDeletingSource(t *testing.T) {
	client := &fakeExecutionClient{}
	handle, err := Acquire(context.Background(), client, Reference{Kind: ReferenceStaticKV, Path: "kv/data/app"})
	if err != nil {
		t.Fatal(err)
	}
	value := handle.Values["token"]
	if err := handle.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.readCalls != 1 || client.revokeCalls != 0 {
		t.Fatalf("read=%d revoke=%d", client.readCalls, client.revokeCalls)
	}
	if err := value.WithBytes(func([]byte) error { return nil }); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("static material remained: %v", err)
	}
}

func TestDynamicHandleRevokesLeaseAndDestroysMaterial(t *testing.T) {
	client := &fakeExecutionClient{}
	handle, err := Acquire(context.Background(), client, Reference{
		Kind: ReferencePostgreSQLDynamic, Path: "database/creds/app", TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	username := handle.Values["username"]
	if err := handle.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if client.revokeCalls != 1 || client.lastLease != "lease-id" {
		t.Fatalf("revoke calls = %d, lease = %q", client.revokeCalls, client.lastLease)
	}
	if err := username.WithBytes(func([]byte) error { return nil }); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("username remained available: %v", err)
	}
}

func TestCertificateAndTransitCannotBeAcquiredAsCredential(t *testing.T) {
	client := &fakeExecutionClient{}
	for _, kind := range []ReferenceKind{ReferenceCertificate, ReferenceTransit} {
		if _, err := Acquire(context.Background(), client, Reference{Kind: kind, Path: "path"}); !errors.Is(err, ErrNotExecutable) {
			t.Errorf("Acquire(%s) error = %v", kind, err)
		}
	}
	if client.readCalls != 0 || client.issueCalls != 0 {
		t.Fatal("non-executable reference touched OpenBao")
	}
}
