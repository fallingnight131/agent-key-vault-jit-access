package proxy_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/agent"
	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/identity"
	"github.com/fallingnight/akv/internal/proxy"
	"github.com/fallingnight/akv/internal/store"
	"github.com/fallingnight/akv/internal/task"
	"github.com/fallingnight/akv/internal/vault"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const e2eOwnerID = "10000000-0000-4000-8000-000000000001"

type e2eVault struct {
	reads atomic.Int32
	value string
}

func (client *e2eVault) ReadKV(context.Context, string, *int) (map[string]*vault.SensitiveValue, error) {
	client.reads.Add(1)
	return map[string]*vault.SensitiveValue{"api_key": vault.NewSensitiveValue([]byte(client.value))}, nil
}

func (*e2eVault) Sign(context.Context, string, string, []byte) ([]byte, error) {
	return nil, errors.New("unexpected sign")
}

func (*e2eVault) IssueDatabase(context.Context, string, time.Duration) (vault.DynamicCredential, error) {
	return vault.DynamicCredential{}, errors.New("unexpected dynamic credential")
}

func (*e2eVault) RevokeLease(context.Context, string) error { return nil }

// TestPostgreSQLEndToEndAuthorizationFlow proves the complete persisted happy
// path without pre-seeding a request or Grant. The protected value and target
// exist only inside this test process.
func TestPostgreSQLEndToEndAuthorizationFlow(t *testing.T) {
	dsn := os.Getenv("AKV_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AKV_TEST_POSTGRES_DSN is not set")
	}
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, `TRUNCATE users, targets CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO users (id,username,password_hash,is_admin,status) VALUES ($1,'e2e-owner','fixture-hash',true,'ACTIVE')`, e2eOwnerID); err != nil {
		t.Fatal(err)
	}

	const protectedValue = "process-only-e2e-value"
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		targetCalls.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/execute" || request.Header.Get("X-API-Key") != protectedValue {
			t.Errorf("unexpected protected target request: method=%s path=%s", request.Method, request.URL.Path)
		}
		response.Header().Set("X-Reflected", protectedValue)
		_, _ = response.Write([]byte("accepted " + protectedValue))
	}))
	defer target.Close()

	agents := agent.NewService(store.NewPostgreSQLAgentRepository(database))
	agentCredential, err := agents.Register(ctx, e2eOwnerID, "e2e-agent", agent.Token24Hours)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := agents.Authenticate(ctx, agentCredential.Token)
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(store.NewPostgreSQLTaskRepository(database))
	taskRecord, err := tasks.Begin(ctx, principal.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	catalogService := catalog.NewService(store.NewPostgreSQLCatalogRepository(database))
	targetRecord, _, err := catalogService.CreateTarget(ctx, identity.User{ID: e2eOwnerID, IsAdmin: true, OwnerActive: true}, catalog.CreateInput{
		Name: "e2e-target", ConnectorType: catalog.ConnectorHTTP,
		ConnectionConfig: catalog.ConnectionConfig{BaseURL: target.URL, AllowedHTTPMethods: []string{http.MethodPost}},
		CredentialAlias:  "default", CredentialType: catalog.CredentialAPIKey, VaultPath: "kv/data/process-only-e2e",
	})
	if err != nil {
		t.Fatal(err)
	}
	requests := store.NewPostgreSQLRequestRepository(database)
	authorizations := authorization.NewService(tasks, catalogService, requests)
	requestRecord, err := authorizations.Submit(ctx, principal, authorization.SubmitInput{
		TaskID: taskRecord.ID, TargetID: targetRecord.ID, Reason: "exercise persisted e2e flow",
		Operation: authorization.Operation{Kind: authorization.OperationHTTP, HTTP: &authorization.HTTPParameters{Method: http.MethodPost, Path: "/execute"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	authorizationRepository := store.NewPostgreSQLAuthorizationRepository(database)
	executions := store.NewPostgreSQLExecutionRepository(database)
	vaultClient := &e2eVault{value: protectedValue}
	httpProxy := proxy.NewHTTPProxy(executions, authorization.NewExecutionGuard(authorizationRepository), vaultClient, executions)
	if _, err := httpProxy.Execute(ctx, requestRecord.ID, principal.AgentID, taskRecord.ID); !errors.Is(err, proxy.ErrExecutionDenied) {
		t.Fatalf("unapproved execution error = %v", err)
	}
	if vaultClient.reads.Load() != 0 || targetCalls.Load() != 0 {
		t.Fatalf("unapproved calls: vault=%d target=%d", vaultClient.reads.Load(), targetCalls.Load())
	}

	approval, grant, err := authorization.NewApprovalService(authorizationRepository).Decide(
		ctx, identity.User{ID: e2eOwnerID, OwnerActive: true}, requestRecord.ID, authorization.DecisionApproved, nil,
	)
	if err != nil || grant == nil {
		t.Fatalf("approve error=%v grant=%v", err, grant)
	}
	result, err := httpProxy.Execute(ctx, requestRecord.ID, principal.AgentID, taskRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if vaultClient.reads.Load() != 1 || targetCalls.Load() != 1 || strings.Contains(string(result.Body), protectedValue) || strings.Contains(result.Headers.Get("X-Reflected"), protectedValue) {
		t.Fatalf("calls vault=%d target=%d result=%q reflected=%q", vaultClient.reads.Load(), targetCalls.Load(), result.Body, result.Headers.Get("X-Reflected"))
	}
	if !strings.Contains(string(result.Body), "[REDACTED]") {
		t.Fatalf("protected response was not redacted: %q", result.Body)
	}

	if _, err := httpProxy.Execute(ctx, requestRecord.ID, principal.AgentID, taskRecord.ID); !errors.Is(err, proxy.ErrExecutionDenied) {
		t.Fatalf("replay execution error = %v", err)
	}
	if vaultClient.reads.Load() != 1 || targetCalls.Load() != 1 {
		t.Fatalf("replay reached protected systems: vault=%d target=%d", vaultClient.reads.Load(), targetCalls.Load())
	}
	var deniedClaims int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE event_type='operation_grants.claim_denied' AND request_id=$1 AND actor_type='AGENT' AND actor_id=$2`, requestRecord.ID, principal.AgentID).Scan(&deniedClaims); err != nil {
		t.Fatal(err)
	}
	if deniedClaims != 1 {
		t.Fatalf("denied claim audit events=%d", deniedClaims)
	}

	var executionID, reclaimID, grantStatus, executionStatus, reclaimStatus string
	err = database.QueryRowContext(ctx, `
SELECT e.id,r.id,g.status,e.status,r.status
FROM operation_grants g
JOIN executions e ON e.grant_id=g.id
JOIN reclaims r ON r.execution_id=e.id
WHERE g.id=$1`, grant.ID).Scan(&executionID, &reclaimID, &grantStatus, &executionStatus, &reclaimStatus)
	if err != nil {
		t.Fatal(err)
	}
	if grantStatus != "RECLAIMED" || executionStatus != "SUCCEEDED" || reclaimStatus != "RECLAIMED" {
		t.Fatalf("grant=%s execution=%s reclaim=%s", grantStatus, executionStatus, reclaimStatus)
	}
	var linked int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE request_id=$1 AND grant_id=$2 AND execution_id=$3 AND reclaim_id=$4`, requestRecord.ID, grant.ID, executionID, reclaimID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked == 0 {
		t.Fatal("reclaim audit event does not link the full authorization chain")
	}
	var approvalEvents int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE request_id=$1 AND approval_id=$2 AND actor_type='USER' AND actor_id=$3`, requestRecord.ID, approval.ID, e2eOwnerID).Scan(&approvalEvents); err != nil {
		t.Fatal(err)
	}
	if approvalEvents != 1 {
		t.Fatalf("approval audit events=%d", approvalEvents)
	}
	status, err := requests.GetAuthorizationStatus(ctx, principal.AgentID, requestRecord.ID)
	if err != nil || status.GrantStatus == nil || *status.GrantStatus != "RECLAIMED" || status.ExecutionStatus == nil || *status.ExecutionStatus != "SUCCEEDED" || status.ReclaimStatus == nil || *status.ReclaimStatus != "RECLAIMED" {
		t.Fatalf("final authorization status=%+v error=%v", status, err)
	}
	if fmt.Sprint(status) == protectedValue {
		t.Fatal("status exposed protected value")
	}
}
