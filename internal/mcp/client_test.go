package mcp

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestClientInjectsProtectedTokenWithoutRetryOrErrorLeak(t *testing.T) {
	calls := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Errorf("authorization=%q", request.Header.Get("Authorization"))
		}
		return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader("backend fixture-secret")), Header: make(http.Header), Request: request}, nil
	})
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("fixture-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(Config{ControlURL: "https://control.example.test", ExecutionURL: "https://execution.example.test", TokenFile: path})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.httpClient.Transport = transport
	_, err = client.ListTargets(context.Background())
	if err == nil || calls != 1 || strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "fixture-token") {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
}

func TestClientExecutesEndpointSelectedByServerOperation(t *testing.T) {
	calls := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		body := ""
		switch request.URL.Path {
		case "/v1/agent/authorizations/request":
			body = `{"request_id":"request","request_status":"APPROVED","approval_deadline":"2026-07-15T00:00:00Z","operation_kind":"HTTP"}`
		case "/v1/execute/http":
			body = `{"status_code":200}`
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: request}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}, Request: request}, nil
	})
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("fixture-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(Config{ControlURL: "https://control.example.test", ExecutionURL: "https://execution.example.test", TokenFile: path})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.httpClient.Transport = transport
	result, err := client.Execute(context.Background(), "request", "task")
	if err != nil || calls != 2 || !strings.Contains(string(result), "status_code") {
		t.Fatalf("result=%s calls=%d error=%v", result, calls, err)
	}
}
