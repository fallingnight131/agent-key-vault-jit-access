package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/domain"
	"github.com/fallingnight/akv/internal/vault"
)

type fakePlans struct{ plan Plan }

func (plans *fakePlans) LoadPlan(context.Context, string) (Plan, error) { return plans.plan, nil }

type fakeGuard struct {
	err   error
	calls int
}

func (guard *fakeGuard) Claim(_ context.Context, claim authorization.ClaimContext) (authorization.Grant, error) {
	guard.calls++
	if guard.err != nil {
		return authorization.Grant{}, guard.err
	}
	return authorization.Grant{ID: claim.GrantID, RequestID: "request", Status: domain.GrantExecuting}, nil
}

type fakeVault struct {
	readCalls int
	values    map[string]*vault.SensitiveValue
}

func (client *fakeVault) ReadKV(context.Context, string, *int) (map[string]*vault.SensitiveValue, error) {
	client.readCalls++
	return client.values, nil
}
func (*fakeVault) Sign(context.Context, string, string, []byte) ([]byte, error) {
	return nil, errors.New("unexpected sign")
}
func (*fakeVault) IssueDatabase(context.Context, string, time.Duration) (vault.DynamicCredential, error) {
	return vault.DynamicCredential{}, errors.New("unexpected issue")
}
func (*fakeVault) RevokeLease(context.Context, string) error { return nil }

type fakeLifecycle struct {
	starts   int
	finishes []domain.ExecutionStatus
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func (lifecycle *fakeLifecycle) Start(context.Context, authorization.Grant, time.Time) (string, error) {
	lifecycle.starts++
	return "execution", nil
}
func (lifecycle *fakeLifecycle) Finish(_ context.Context, _ string, status domain.ExecutionStatus, _ time.Time, _ string) error {
	lifecycle.finishes = append(lifecycle.finishes, status)
	return nil
}

func TestHTTPProxyClaimsBeforeVaultAndTarget(t *testing.T) {
	targetCalls := 0
	plan := validPlan("https://target.example.test")
	guard := &fakeGuard{err: authorization.ErrClaimDenied}
	vaultClient := &fakeVault{values: map[string]*vault.SensitiveValue{"api_key": vault.NewSensitiveValue([]byte("fixture-api-key"))}}
	lifecycle := &fakeLifecycle{}
	proxy := NewHTTPProxy(&fakePlans{plan}, guard, vaultClient, lifecycle)
	proxy.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		targetCalls++
		return nil, errors.New("unexpected target call")
	})

	_, err := proxy.Execute(context.Background(), "request", "agent", "task")
	if !errors.Is(err, ErrExecutionDenied) {
		t.Fatalf("Execute() error = %v", err)
	}
	if guard.calls != 1 || vaultClient.readCalls != 0 || targetCalls != 0 || lifecycle.starts != 0 {
		t.Fatalf("calls guard=%d vault=%d target=%d lifecycle=%d", guard.calls, vaultClient.readCalls, targetCalls, lifecycle.starts)
	}
}

func TestHTTPProxyInjectsOnceAndRedactsReflectedSecret(t *testing.T) {
	const secret = "fixture-api-key"
	targetCalls := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		targetCalls++
		if request.Header.Get("X-API-Key") != secret {
			t.Errorf("X-API-Key = %q", request.Header.Get("X-API-Key"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"X-Reflected": []string{secret}},
			Body:       io.NopCloser(strings.NewReader("target reflected " + secret)),
			Request:    request,
		}, nil
	})
	guard := &fakeGuard{}
	vaultClient := &fakeVault{values: map[string]*vault.SensitiveValue{"api_key": vault.NewSensitiveValue([]byte(secret))}}
	lifecycle := &fakeLifecycle{}
	proxy := NewHTTPProxy(&fakePlans{validPlan("https://target.example.test")}, guard, vaultClient, lifecycle)
	proxy.client.Transport = transport

	result, err := proxy.Execute(context.Background(), "request", "agent", "task")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if targetCalls != 1 || vaultClient.readCalls != 1 {
		t.Fatalf("target calls=%d vault calls=%d", targetCalls, vaultClient.readCalls)
	}
	if strings.Contains(string(result.Body), secret) || strings.Contains(result.Headers.Get("X-Reflected"), secret) {
		t.Fatalf("result leaked secret: body=%q header=%q", result.Body, result.Headers.Get("X-Reflected"))
	}
	if !strings.Contains(string(result.Body), "[REDACTED]") || len(lifecycle.finishes) != 1 || lifecycle.finishes[0] != domain.ExecutionSucceeded {
		t.Fatalf("result=%q lifecycle=%v", result.Body, lifecycle.finishes)
	}
	if err := vaultClient.values["api_key"].WithBytes(func([]byte) error { return nil }); !errors.Is(err, vault.ErrUnavailable) {
		t.Fatal("credential material was not destroyed")
	}
}

func TestHTTPProxyDoesNotFollowRedirect(t *testing.T) {
	destinationCalls := 0
	proxy := NewHTTPProxy(
		&fakePlans{validPlan("https://source.example.test")}, &fakeGuard{},
		&fakeVault{values: map[string]*vault.SensitiveValue{"api_key": vault.NewSensitiveValue([]byte("fixture-api-key"))}},
		&fakeLifecycle{},
	)
	proxy.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "destination.example.test" {
			destinationCalls++
		}
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"https://destination.example.test/stolen"}},
			Body:       io.NopCloser(strings.NewReader("redirect")),
			Request:    request,
		}, nil
	})
	result, err := proxy.Execute(context.Background(), "request", "agent", "task")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.StatusCode != http.StatusFound || destinationCalls != 0 {
		t.Fatalf("status=%d destination calls=%d", result.StatusCode, destinationCalls)
	}
}

func validPlan(baseURL string) Plan {
	var operationHash [32]byte
	operationHash[0] = 1
	return Plan{
		RequestID: "request", GrantID: "grant", AgentID: "agent", TaskID: "task",
		Target: catalog.Target{
			ID: "target", ConnectorType: catalog.ConnectorHTTP,
			ConnectionConfig: catalog.ConnectionConfig{BaseURL: baseURL, AllowedHTTPMethods: []string{"POST"}}, Active: true,
		},
		Credential: catalog.Credential{ID: "credential", Type: catalog.CredentialAPIKey, VaultPath: "kv/data/fixture", Active: true},
		Operation: authorization.Operation{
			Kind: authorization.OperationHTTP,
			HTTP: &authorization.HTTPParameters{Method: "POST", Path: "/execute"},
		},
		OperationHash: operationHash,
	}
}
