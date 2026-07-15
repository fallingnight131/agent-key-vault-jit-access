package vault

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestOpenBaoRequiresProtectedTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("fixture-token"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewOpenBaoExecutionClient("https://bao.example.test", path); err == nil {
		t.Fatal("NewOpenBaoExecutionClient() accepted group-readable token")
	}
}

func TestOpenBaoExecutionOperationsUseTokenWithoutLeakingErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("fixture-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := NewOpenBaoExecutionClient("https://bao.example.test", path)
	if err != nil {
		t.Fatalf("NewOpenBaoExecutionClient() error = %v", err)
	}
	defer client.Close()
	requests := 0
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Header.Get("X-Vault-Token") != "fixture-token" {
			t.Errorf("vault token header = %q", request.Header.Get("X-Vault-Token"))
		}
		body := `{"data":{"data":{"api_key":"fixture-secret"}}}`
		switch request.URL.Path {
		case "/v1/transit/sign/signing":
			body = `{"data":{"signature":"vault:v1:fixture-signature"}}`
		case "/v1/database/creds/app":
			body = `{"lease_id":"lease","lease_duration":60,"data":{"username":"temporary-user","password":"temporary-password"}}`
		case "/v1/sys/leases/revoke":
			body = `{}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: request}, nil
	})
	values, err := client.ReadKV(context.Background(), "kv/data/app", nil)
	if err != nil || values["api_key"] == nil {
		t.Fatalf("ReadKV() values=%v error=%v", values, err)
	}
	signature, err := client.Sign(context.Background(), "transit/keys/signing", "sha2-256", []byte("digest"))
	if err != nil || string(signature) != "vault:v1:fixture-signature" {
		t.Fatalf("Sign() signature=%q error=%v", signature, err)
	}
	credential, err := client.IssueDatabase(context.Background(), "database/creds/app", time.Minute)
	if err != nil || credential.LeaseID != "lease" {
		t.Fatalf("IssueDatabase() credential=%+v error=%v", credential, err)
	}
	if err := client.RevokeLease(context.Background(), credential.LeaseID); err != nil {
		t.Fatalf("RevokeLease() error = %v", err)
	}
	if requests != 4 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestOpenBaoErrorBodyIsNeverReturned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("fixture-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := NewOpenBaoExecutionClient("https://bao.example.test", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader("backend leaked fixture-secret")), Header: make(http.Header), Request: request}, nil
	})
	_, err = client.ReadKV(context.Background(), "kv/data/app", nil)
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatalf("ReadKV() error = %v", err)
	}
}

func TestOpenBaoControlClientOnlyWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("control-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := NewOpenBaoControlClient("https://bao.example.test", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	requests := 0
	client.client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodPost || request.Header.Get("X-Vault-Token") != "control-token" {
			t.Fatalf("request method=%s token=%q", request.Method, request.Header.Get("X-Vault-Token"))
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if request.URL.Path == "/v1/kv/data/app" {
			data, _ := payload["data"].(map[string]any)
			if data["api_key"] != "fixture-secret" {
				t.Fatalf("KV payload = %#v", payload)
			}
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: request}, nil
	})
	secret := NewSensitiveValue([]byte("fixture-secret"))
	defer secret.Destroy()
	if err := client.WriteKV(context.Background(), KVWrite{Path: "kv/data/app", Values: map[string]*SensitiveValue{"api_key": secret}}); err != nil {
		t.Fatalf("WriteKV() error = %v", err)
	}
	if err := client.ConfigureDatabaseRole(context.Background(), DatabaseRole{Name: "app", ConnectionName: "postgres", CreationStatements: []string{"CREATE ROLE"}, DefaultTTL: time.Minute, MaxTTL: 10 * time.Minute}); err != nil {
		t.Fatalf("ConfigureDatabaseRole() error = %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d", requests)
	}
}

var _ ControlWriter = (*OpenBaoControlClient)(nil)
