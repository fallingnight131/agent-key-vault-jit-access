package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/domain"
	"github.com/fallingnight/akv/internal/vault"
)

const (
	HTTPTimeout     = 30 * time.Second
	maxResponseBody = 1 << 20
)

var ErrExecutionDenied = errors.New("execution denied")

type PublicError struct {
	Code string
}

func (failure *PublicError) Error() string { return failure.Code }

type Plan struct {
	RequestID     string
	GrantID       string
	AgentID       string
	TaskID        string
	Target        catalog.Target
	Credential    catalog.Credential
	Operation     authorization.Operation
	OperationHash [32]byte
}

type PlanStore interface {
	LoadPlan(context.Context, string) (Plan, error)
}

type Guard interface {
	Claim(context.Context, authorization.ClaimContext) (authorization.Grant, error)
}

type Lifecycle interface {
	Start(context.Context, authorization.Grant, time.Time) (string, error)
	Finish(context.Context, string, domain.ExecutionStatus, time.Time, string) error
	StartReclaim(context.Context, string, time.Time) (string, error)
	FinishReclaim(context.Context, string, bool, time.Time, string) error
}

type HTTPResult struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

type HTTPProxy struct {
	plans     PlanStore
	guard     Guard
	vault     vault.ExecutionClient
	lifecycle Lifecycle
	client    *http.Client
	now       func() time.Time
}

func NewHTTPProxy(plans PlanStore, guard Guard, vaultClient vault.ExecutionClient, lifecycle Lifecycle) *HTTPProxy {
	return &HTTPProxy{
		plans: plans, guard: guard, vault: vaultClient, lifecycle: lifecycle,
		client: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now: time.Now,
	}
}

func (proxy *HTTPProxy) Execute(ctx context.Context, requestID, authenticatedAgentID, taskID string) (HTTPResult, error) {
	if requestID == "" || authenticatedAgentID == "" || taskID == "" {
		return HTTPResult{}, ErrExecutionDenied
	}
	plan, err := proxy.plans.LoadPlan(ctx, requestID)
	if err != nil || plan.AgentID != authenticatedAgentID || plan.TaskID != taskID || plan.Operation.Kind != authorization.OperationHTTP || plan.Operation.HTTP == nil {
		return HTTPResult{}, ErrExecutionDenied
	}
	claim := authorization.ClaimContext{
		GrantID: plan.GrantID, AgentID: authenticatedAgentID, TaskID: taskID,
		TargetID: plan.Target.ID, CredentialID: plan.Credential.ID, OperationHash: plan.OperationHash,
	}
	grant, err := proxy.guard.Claim(ctx, claim)
	if err != nil {
		return HTTPResult{}, ErrExecutionDenied
	}
	executionID, err := proxy.lifecycle.Start(ctx, grant, proxy.now())
	if err != nil {
		return HTTPResult{}, &PublicError{Code: "EXECUTION_STATE_FAILED"}
	}

	handle, err := vault.Acquire(ctx, proxy.vault, vault.Reference{
		Kind: vault.ReferenceStaticKV, Path: plan.Credential.VaultPath, Version: plan.Credential.VaultVersion,
	})
	if err != nil {
		if finalError := finalizeExecution(ctx, proxy.lifecycle, executionID, domain.ExecutionFailed, "VAULT_UNAVAILABLE", nil, proxy.now); finalError != nil {
			return HTTPResult{}, finalError
		}
		return HTTPResult{}, &PublicError{Code: "VAULT_UNAVAILABLE"}
	}
	redactor := NewRedactor(handle.Values)
	cleanup := func(cleanupContext context.Context) error {
		redactor.Destroy()
		return handle.Close(cleanupContext)
	}

	request, err := buildHTTPRequest(ctx, plan, handle.Values)
	if err != nil {
		if finalError := finalizeExecution(ctx, proxy.lifecycle, executionID, domain.ExecutionFailed, "INVALID_EXECUTION_PLAN", cleanup, proxy.now); finalError != nil {
			return HTTPResult{}, finalError
		}
		return HTTPResult{}, &PublicError{Code: "INVALID_EXECUTION_PLAN"}
	}
	timeoutContext, cancel := context.WithTimeout(request.Context(), HTTPTimeout)
	defer cancel()
	request = request.WithContext(timeoutContext)
	response, err := proxy.client.Do(request)
	clearInjectedHeaders(request.Header)
	if err != nil {
		status := domain.ExecutionFailed
		code := "TARGET_UNAVAILABLE"
		if errors.Is(timeoutContext.Err(), context.DeadlineExceeded) {
			status, code = domain.ExecutionTimedOut, "TARGET_TIMEOUT"
		}
		if finalError := finalizeExecution(ctx, proxy.lifecycle, executionID, status, code, cleanup, proxy.now); finalError != nil {
			return HTTPResult{}, finalError
		}
		return HTTPResult{}, &PublicError{Code: code}
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBody+1))
	if readErr != nil || len(body) > maxResponseBody {
		if finalError := finalizeExecution(ctx, proxy.lifecycle, executionID, domain.ExecutionFailed, "RESPONSE_INVALID", cleanup, proxy.now); finalError != nil {
			return HTTPResult{}, finalError
		}
		return HTTPResult{}, &PublicError{Code: "RESPONSE_INVALID"}
	}
	result := HTTPResult{StatusCode: response.StatusCode, Headers: redactHeaders(redactor, response.Header), Body: redactor.Bytes(body)}
	if err := finalizeExecution(ctx, proxy.lifecycle, executionID, domain.ExecutionSucceeded, "", cleanup, proxy.now); err != nil {
		return HTTPResult{}, err
	}
	return result, nil
}

func buildHTTPRequest(ctx context.Context, plan Plan, values map[string]*vault.SensitiveValue) (*http.Request, error) {
	base, err := url.Parse(plan.Target.ConnectionConfig.BaseURL)
	if err != nil {
		return nil, err
	}
	parameters := plan.Operation.HTTP
	relative, err := url.Parse(parameters.Path)
	if err != nil || relative.IsAbs() || relative.Host != "" {
		return nil, ErrExecutionDenied
	}
	destination := base.ResolveReference(relative)
	query := destination.Query()
	for name, entries := range parameters.Query {
		for _, entry := range entries {
			query.Add(name, entry)
		}
	}
	destination.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, parameters.Method, destination.String(), bytes.NewReader(parameters.Body))
	if err != nil {
		return nil, err
	}
	for name, value := range parameters.Headers {
		request.Header.Set(name, value)
	}
	switch plan.Credential.Type {
	case catalog.CredentialAPIKey:
		err = setHeaderFromValue(request.Header, "X-API-Key", values["api_key"], "")
	case catalog.CredentialAccessToken:
		err = setHeaderFromValue(request.Header, "Authorization", values["access_token"], "Bearer ")
	case catalog.CredentialUsernamePassword:
		var username, password string
		if values["username"] == nil || values["password"] == nil {
			return nil, ErrExecutionDenied
		}
		_ = values["username"].WithBytes(func(value []byte) error { username = string(value); return nil })
		_ = values["password"].WithBytes(func(value []byte) error { password = string(value); return nil })
		request.SetBasicAuth(username, password)
		username, password = "", ""
	default:
		return nil, ErrExecutionDenied
	}
	if err != nil {
		return nil, err
	}
	return request, nil
}

func setHeaderFromValue(headers http.Header, name string, value *vault.SensitiveValue, prefix string) error {
	if value == nil {
		return ErrExecutionDenied
	}
	return value.WithBytes(func(secret []byte) error {
		headers.Set(name, prefix+string(secret))
		return nil
	})
}

func clearInjectedHeaders(headers http.Header) {
	headers.Del("Authorization")
	headers.Del("Proxy-Authorization")
	headers.Del("X-API-Key")
}

func redactHeaders(redactor *Redactor, headers http.Header) http.Header {
	result := make(http.Header, len(headers))
	for name, values := range headers {
		for _, value := range values {
			result.Add(name, redactor.String(value))
		}
	}
	return result
}

func (result HTTPResult) String() string {
	return fmt.Sprintf("HTTP %d (%d bytes)", result.StatusCode, len(result.Body))
}
