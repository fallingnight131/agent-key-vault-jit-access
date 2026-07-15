package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/control"
	"github.com/fallingnight/akv/internal/domain"
	"github.com/fallingnight/akv/internal/proxy"
)

const maxResponse = 1 << 20

type Config struct {
	ControlURL   string
	ExecutionURL string
	TokenFile    string
}
type Client struct {
	control    *url.URL
	execution  *url.URL
	token      []byte
	httpClient *http.Client
}

func NewClient(config Config) (*Client, error) {
	controlURL, err := validateBaseURL(config.ControlURL)
	if err != nil {
		return nil, err
	}
	executionURL, err := validateBaseURL(config.ExecutionURL)
	if err != nil {
		return nil, err
	}
	token, err := proxy.ReadProtectedConfigFile(config.TokenFile)
	if err != nil {
		return nil, err
	}
	return &Client{control: controlURL, execution: executionURL, token: token, httpClient: &http.Client{Timeout: 6 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func validateBaseURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimRight(value, "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid AKV service URL")
	}
	return parsed, nil
}
func (client *Client) Close() {
	for index := range client.token {
		client.token[index] = 0
	}
	client.token = nil
}

type Target struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	ConnectorType      string   `json:"connector_type"`
	AllowedHTTPMethods []string `json:"allowed_http_methods,omitempty"`
}
type Task struct {
	TaskID                   string `json:"task_id"`
	Status                   string `json:"status"`
	HeartbeatIntervalSeconds int    `json:"heartbeat_interval_seconds"`
}
type RequestResult struct {
	RequestID        string    `json:"request_id"`
	Status           string    `json:"status"`
	ApprovalDeadline time.Time `json:"approval_deadline"`
}

func (client *Client) ListTargets(ctx context.Context) ([]Target, error) {
	var result []Target
	return result, client.call(ctx, client.control, http.MethodGet, "/v1/agent/targets", nil, &result)
}
func (client *Client) BeginTask(ctx context.Context) (Task, error) {
	var result Task
	return result, client.call(ctx, client.control, http.MethodPost, "/v1/agent/tasks", map[string]any{}, &result)
}
func (client *Client) Heartbeat(ctx context.Context, taskID string) error {
	return client.call(ctx, client.control, http.MethodPost, "/v1/agent/tasks/"+url.PathEscape(taskID)+"/heartbeat", map[string]any{}, nil)
}
func (client *Client) EndTask(ctx context.Context, taskID string, outcome domain.TaskStatus) error {
	return client.call(ctx, client.control, http.MethodPost, "/v1/agent/tasks/"+url.PathEscape(taskID)+"/end", map[string]any{"outcome": outcome}, nil)
}
func (client *Client) RequestAuthorization(ctx context.Context, input authorization.SubmitInput) (RequestResult, error) {
	var result RequestResult
	return result, client.call(ctx, client.control, http.MethodPost, "/v1/agent/authorizations", input, &result)
}
func (client *Client) Status(ctx context.Context, requestID string) (control.AuthorizationStatus, error) {
	var result control.AuthorizationStatus
	return result, client.call(ctx, client.control, http.MethodGet, "/v1/agent/authorizations/"+url.PathEscape(requestID), nil, &result)
}
func (client *Client) Cancel(ctx context.Context, requestID string) error {
	return client.call(ctx, client.control, http.MethodPost, "/v1/agent/authorizations/"+url.PathEscape(requestID)+"/revoke", map[string]any{}, nil)
}
func (client *Client) Execute(ctx context.Context, requestID, taskID string) (json.RawMessage, error) {
	status, err := client.Status(ctx, requestID)
	if err != nil {
		return nil, err
	}
	endpoint := ""
	switch authorization.OperationKind(status.OperationKind) {
	case authorization.OperationHTTP:
		endpoint = "/v1/execute/http"
	case authorization.OperationPostgreSQLStatement, authorization.OperationPostgreSQLBatch:
		endpoint = "/v1/execute/postgresql"
	case authorization.OperationSign:
		endpoint = "/v1/execute/sign"
	default:
		return nil, errors.New("unsupported operation")
	}
	var result json.RawMessage
	return result, client.call(ctx, client.execution, http.MethodPost, endpoint, map[string]string{"request_id": requestID, "task_id": taskID}, &result)
}

func (client *Client) call(ctx context.Context, base *url.URL, method, path string, payload, output any) error {
	endpoint := *base
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return errors.New("invalid request")
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return errors.New("request unavailable")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+string(client.token))
	response, err := client.httpClient.Do(request)
	request.Header.Del("Authorization")
	if err != nil {
		return errors.New("AKV service unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponse))
		return fmt.Errorf("AKV request rejected (%d)", response.StatusCode)
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponse))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponse)).Decode(output); err != nil {
		return errors.New("invalid AKV response")
	}
	return nil
}
