package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/agent"
	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/domain"
	"github.com/fallingnight/akv/internal/identity"
	"github.com/fallingnight/akv/internal/lifecycle"
	"github.com/fallingnight/akv/internal/task"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgreSQLAgentControlFlow(t *testing.T) {
	dsn := os.Getenv("AKV_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AKV_TEST_POSTGRES_DSN is not set")
	}
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer database.Close()
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, `TRUNCATE targets, users CASCADE`); err != nil {
		t.Fatalf("truncate database: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO users (id,username,password_hash,is_admin,status) VALUES ($1,'admin','fixture-hash',true,'ACTIVE')`, testUserID); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	agentService := agent.NewService(NewPostgreSQLAgentRepository(database))
	agentCredential, err := agentService.Register(ctx, testUserID, "control-flow-agent", agent.Token24Hours)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	principal, err := agentService.Authenticate(ctx, agentCredential.Token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	agents, err := agentService.List(ctx, testUserID)
	if err != nil || len(agents) != 1 || agents[0].ID != principal.AgentID || agents[0].TokenExpiresAt == nil {
		t.Fatalf("List() agents=%+v error=%v", agents, err)
	}

	catalogService := catalog.NewService(NewPostgreSQLCatalogRepository(database))
	target, credential, err := catalogService.CreateTarget(ctx, identity.User{ID: testUserID, IsAdmin: true, OwnerActive: true}, catalog.CreateInput{
		Name: "control-flow-target", Description: "integration fixture",
		ConnectorType: catalog.ConnectorHTTP,
		ConnectionConfig: catalog.ConnectionConfig{
			BaseURL: "https://target.example.test", AllowedHTTPMethods: []string{"POST"},
		},
		CredentialAlias: "default", CredentialType: catalog.CredentialAPIKey,
		VaultPath: "kv/data/control-flow",
	})
	if err != nil {
		t.Fatalf("CreateTarget() error = %v", err)
	}
	adminActor := identity.User{ID: testUserID, IsAdmin: true, OwnerActive: true}
	operationSet, err := catalogService.CreateOperationSet(ctx, adminActor, catalog.CreateOperationSetInput{Name: "control-flow-http", ExecutorType: catalog.ExecutorHTTP})
	if err != nil {
		t.Fatalf("CreateOperationSet() error = %v", err)
	}
	safeOperation, operationVersion, err := catalogService.CreateOperation(ctx, adminActor, operationSet.ID, catalog.PublishOperationInput{
		Key: "create_ticket", Name: "Create ticket", RiskLevel: catalog.RiskMedium,
		ArgumentsSchema:   []byte(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
		ExecutionTemplate: []byte(`{"kind":"HTTP","http":{"method":"POST","path":"/tickets"}}`),
	})
	if err != nil {
		t.Fatalf("CreateOperation() error = %v", err)
	}
	if _, err := catalogService.BindOperation(ctx, adminActor, target.ID, safeOperation.ID, operationVersion.Version, true); err != nil {
		t.Fatalf("BindOperation() error = %v", err)
	}
	targets, credentials, err := catalogService.ListCatalog(ctx, adminActor)
	if err != nil || len(targets) != 1 || len(credentials) != 1 || credentials[0].ID != credential.ID {
		t.Fatalf("ListCatalog() targets=%+v credentials=%+v error=%v", targets, credentials, err)
	}
	updatedConfiguration := target.ConnectionConfig
	updatedConfiguration.AllowedHTTPMethods = []string{"POST", "PATCH"}
	if err := catalogService.UpdateTarget(ctx, adminActor, target.ID, "updated", updatedConfiguration); err != nil {
		t.Fatalf("UpdateTarget() error = %v", err)
	}
	discovered, err := catalogService.Discover(ctx, principal.AgentID)
	if err != nil || len(discovered) != 1 || discovered[0].ID != target.ID {
		t.Fatalf("Discover() targets=%+v error=%v", discovered, err)
	}

	taskService := task.NewService(NewPostgreSQLTaskRepository(database))
	taskRecord, err := taskService.Begin(ctx, principal.AgentID)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if err := taskService.Heartbeat(ctx, principal.AgentID, taskRecord.ID); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	requestRepository := NewPostgreSQLRequestRepository(database)
	requestService := authorization.NewService(taskService, catalogService, requestRepository)
	requestRecord, err := requestService.Submit(ctx, principal, authorization.SubmitInput{
		TaskID: taskRecord.ID, TargetID: target.ID, Reason: "integration fixture",
		OperationID: safeOperation.ID, Version: operationVersion.Version, Arguments: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if requestRecord.CredentialID != credential.ID {
		t.Fatalf("credential ID = %q, want server-owned %q", requestRecord.CredentialID, credential.ID)
	}
	status, err := requestRepository.GetAuthorizationStatus(ctx, principal.AgentID, requestRecord.ID)
	if err != nil || status.RequestStatus != string(domain.RequestPendingApproval) {
		t.Fatalf("GetAuthorizationStatus() status=%+v error=%v", status, err)
	}
	if _, err := requestRepository.GetAuthorizationStatus(ctx, "00000000-0000-4000-8000-000000000099", requestRecord.ID); err == nil {
		t.Fatal("cross-agent status lookup succeeded")
	}
	webRepository := NewPostgreSQLWebRepository(database)
	requests, err := webRepository.ListAuthorizationRequests(ctx, adminActor)
	if err != nil || len(requests) != 1 || requests[0].CredentialAlias != "default" || len(requests[0].Operation) == 0 {
		t.Fatalf("ListAuthorizationRequests() requests=%+v error=%v", requests, err)
	}
	const unrelatedUserID = "00000000-0000-4000-8000-000000000088"
	if _, err := database.ExecContext(ctx, `INSERT INTO users (id,username,password_hash,status) VALUES ($1,'unrelated','fixture-hash','ACTIVE')`, unrelatedUserID); err != nil {
		t.Fatalf("seed unrelated user: %v", err)
	}
	unrelatedRequests, err := webRepository.ListAuthorizationRequests(ctx, identity.User{ID: unrelatedUserID, OwnerActive: true})
	if err != nil || len(unrelatedRequests) != 0 {
		t.Fatalf("unrelated ListAuthorizationRequests() requests=%+v error=%v", unrelatedRequests, err)
	}
	ttl := time.Minute
	if _, _, err := authorization.NewApprovalService(NewPostgreSQLAuthorizationRepository(database)).Decide(ctx, adminActor, requestRecord.ID, authorization.DecisionApproved, &ttl); err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	auditEvents, err := webRepository.ReadAuthorizationAudit(ctx, adminActor, requestRecord.ID)
	if err != nil || len(auditEvents) < 2 {
		t.Fatalf("ReadAuthorizationAudit() events=%+v error=%v", auditEvents, err)
	}
	if _, err := lifecycle.NewService(NewPostgreSQLLifecycleRepository(database)).Revoke(ctx, adminActor, requestRecord.ID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if _, err := taskService.End(ctx, principal.AgentID, taskRecord.ID, domain.TaskCompleted); err != nil {
		t.Fatalf("End() error = %v", err)
	}
	if err := taskService.Heartbeat(ctx, principal.AgentID, taskRecord.ID); !errors.Is(err, task.ErrTaskUnavailable) {
		t.Fatalf("terminal Heartbeat() error = %v", err)
	}
	if err := agentService.RevokeToken(ctx, testUserID, principal.AgentID); err != nil {
		t.Fatalf("RevokeToken() error = %v", err)
	}
	replacement, err := agentService.RotateToken(ctx, testUserID, principal.AgentID, agent.Token24Hours)
	if err != nil {
		t.Fatalf("RotateToken() after revocation error = %v", err)
	}
	if _, err := agentService.Authenticate(ctx, replacement.Token); err != nil {
		t.Fatalf("replacement Authenticate() error = %v", err)
	}
	if err := catalogService.SetCredentialActive(ctx, adminActor, credential.ID, false); err != nil {
		t.Fatalf("SetCredentialActive() error = %v", err)
	}
	if _, _, err := catalogService.ResolveForRequest(ctx, target.ID); !errors.Is(err, catalog.ErrUnavailable) {
		t.Fatalf("disabled credential ResolveForRequest() error = %v", err)
	}
	if err := catalogService.SetTargetActive(ctx, adminActor, target.ID, false); err != nil {
		t.Fatalf("SetTargetActive() error = %v", err)
	}
}
