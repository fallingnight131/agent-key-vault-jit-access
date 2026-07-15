package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/fallingnight/akv/internal/agent"
	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/domain"
	"github.com/fallingnight/akv/internal/identity"
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
		Operation: authorization.Operation{Kind: authorization.OperationHTTP, HTTP: &authorization.HTTPParameters{Method: "POST", Path: "/tickets"}},
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
	if _, err := taskService.End(ctx, principal.AgentID, taskRecord.ID, domain.TaskCompleted); err != nil {
		t.Fatalf("End() error = %v", err)
	}
	if err := taskService.Heartbeat(ctx, principal.AgentID, taskRecord.ID); !errors.Is(err, task.ErrTaskUnavailable) {
		t.Fatalf("terminal Heartbeat() error = %v", err)
	}
}
