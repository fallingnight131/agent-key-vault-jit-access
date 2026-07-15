package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/agent"
	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/domain"
	"github.com/fallingnight/akv/internal/identity"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgreSQLAuthorizationConcurrency(t *testing.T) {
	dsn := os.Getenv("AKV_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AKV_TEST_POSTGRES_DSN is not set")
	}
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer database.Close()
	if err := database.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	seedAuthorizationDatabase(t, database)
	if _, err := database.Exec(`UPDATE authorization_requests SET reason='mutated' WHERE id=$1`, testRequestID); err == nil {
		t.Fatal("immutable authorization snapshot update succeeded")
	}
	agentService := agent.NewService(NewPostgreSQLAgentRepository(database))
	credential, err := agentService.Register(context.Background(), testUserID, "route-agent", agent.Token24Hours)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if principal, err := agentService.Authenticate(context.Background(), credential.Token); err != nil || principal.AgentID != credential.AgentID {
		t.Fatalf("Authenticate() principal=%+v error=%v", principal, err)
	}
	replacement, err := agentService.RotateToken(context.Background(), testUserID, credential.AgentID, agent.Token30Days)
	if err != nil {
		t.Fatalf("RotateToken() error = %v", err)
	}
	if _, err := agentService.Authenticate(context.Background(), credential.Token); !errors.Is(err, agent.ErrUnauthorized) {
		t.Fatalf("old token Authenticate() error = %v", err)
	}
	if _, err := agentService.Authenticate(context.Background(), replacement.Token); err != nil {
		t.Fatalf("replacement Authenticate() error = %v", err)
	}

	repository := NewPostgreSQLAuthorizationRepository(database)
	service := authorization.NewApprovalService(repository)
	actor := identity.User{ID: testUserID, OwnerActive: true}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, decision := range []authorization.Decision{authorization.DecisionApproved, authorization.DecisionRejected} {
		wait.Add(1)
		go func(decision authorization.Decision) {
			defer wait.Done()
			<-start
			_, _, err := service.Decide(context.Background(), actor, testRequestID, decision, nil)
			results <- err
		}(decision)
	}
	close(start)
	wait.Wait()
	close(results)
	winners, conflicts := 0, 0
	for err := range results {
		if err == nil {
			winners++
		} else if errors.Is(err, authorization.ErrDecisionConflict) {
			conflicts++
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("approval winners=%d conflicts=%d", winners, conflicts)
	}
	assertDecisionRows(t, database)

	seedApprovedGrant(t, database)
	executionRepository := NewPostgreSQLExecutionRepository(database)
	plan, err := executionRepository.LoadPlan(context.Background(), testClaimRequest)
	if err != nil {
		t.Fatalf("LoadPlan() error = %v", err)
	}
	if plan.GrantID != testGrantID || plan.Credential.ID != testCredentialID || plan.Operation.Kind != authorization.OperationHTTP {
		t.Fatalf("loaded plan = %+v", plan)
	}
	guard := authorization.NewExecutionGuard(repository)
	claim := authorization.ClaimContext{
		GrantID: testGrantID, AgentID: testAgentID, TaskID: testTaskID,
		TargetID: testTargetID, CredentialID: testCredentialID, OperationHash: testOperationHash,
	}
	const contenders = 24
	start = make(chan struct{})
	claimResults := make(chan error, contenders)
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := guard.Claim(context.Background(), claim)
			claimResults <- err
		}()
	}
	close(start)
	wait.Wait()
	close(claimResults)
	winners, conflicts = 0, 0
	for err := range claimResults {
		if err == nil {
			winners++
		} else if errors.Is(err, authorization.ErrClaimDenied) {
			conflicts++
		}
	}
	if winners != 1 || conflicts != contenders-1 {
		t.Fatalf("claim winners=%d denied=%d", winners, conflicts)
	}
	executionID, err := executionRepository.Start(context.Background(), authorization.Grant{ID: testGrantID}, time.Now().UTC())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := executionRepository.Finish(context.Background(), executionID, domain.ExecutionSucceeded, time.Now().UTC(), ""); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	var executionStatus, grantStatus string
	if err := database.QueryRow(`SELECT e.status, g.status FROM executions e JOIN operation_grants g ON g.id=e.grant_id WHERE e.id=$1`, executionID).Scan(&executionStatus, &grantStatus); err != nil {
		t.Fatalf("read execution lifecycle: %v", err)
	}
	if executionStatus != "SUCCEEDED" || grantStatus != "SUCCEEDED" {
		t.Fatalf("execution status=%s grant status=%s", executionStatus, grantStatus)
	}
	reclaimID, err := executionRepository.StartReclaim(context.Background(), executionID, time.Now().UTC())
	if err != nil {
		t.Fatalf("StartReclaim() error = %v", err)
	}
	if err := executionRepository.FinishReclaim(context.Background(), reclaimID, false, time.Now().UTC(), "FIXTURE_CLEANUP_FAILED"); err != nil {
		t.Fatalf("FinishReclaim() error = %v", err)
	}
	var reclaimStatus string
	var incidents int
	if err := database.QueryRow(`SELECT r.status,g.status,(SELECT count(*) FROM security_incidents i WHERE i.reclaim_id=r.id) FROM reclaims r JOIN executions e ON e.id=r.execution_id JOIN operation_grants g ON g.id=e.grant_id WHERE r.id=$1`, reclaimID).Scan(&reclaimStatus, &grantStatus, &incidents); err != nil {
		t.Fatalf("read reclaim lifecycle: %v", err)
	}
	if reclaimStatus != "RECLAIM_FAILED" || grantStatus != "RECLAIM_FAILED" || incidents != 1 {
		t.Fatalf("reclaim=%s grant=%s incidents=%d", reclaimStatus, grantStatus, incidents)
	}
	var incidentID string
	if err := database.QueryRow(`SELECT id FROM security_incidents WHERE reclaim_id=$1`, reclaimID).Scan(&incidentID); err != nil {
		t.Fatalf("read incident: %v", err)
	}
	if err := NewPostgreSQLWebRepository(database).ResolveSecurityIncident(context.Background(), identity.User{ID: testUserID, IsAdmin: true, OwnerActive: true}, incidentID); err != nil {
		t.Fatalf("ResolveSecurityIncident() error = %v", err)
	}
	if err := database.QueryRow(`SELECT status FROM operation_grants WHERE id=$1`, testGrantID).Scan(&grantStatus); err != nil || grantStatus != "RECLAIM_FAILED" {
		t.Fatalf("resolved incident restored grant status=%s error=%v", grantStatus, err)
	}
	if _, err := guard.Claim(context.Background(), claim); !errors.Is(err, authorization.ErrClaimDenied) {
		t.Fatalf("replay Claim() error = %v", err)
	}
}

var testOperationHash = [32]byte{1, 2, 3, 4}
var testDefinitionHash = [32]byte{5, 6, 7, 8}

const (
	testUserID       = "00000000-0000-4000-8000-000000000001"
	testAgentID      = "00000000-0000-4000-8000-000000000002"
	testTaskID       = "00000000-0000-7000-8000-000000000003"
	testTargetID     = "00000000-0000-4000-8000-000000000004"
	testCredentialID = "00000000-0000-4000-8000-000000000005"
	testRequestID    = "00000000-0000-4000-8000-000000000006"
	testClaimRequest = "00000000-0000-4000-8000-000000000007"
	testGrantID      = "00000000-0000-4000-8000-000000000008"
	testOperationSet = "00000000-0000-4000-8000-000000000009"
	testOperationID  = "00000000-0000-4000-8000-000000000010"
)

func seedAuthorizationDatabase(t *testing.T, database *sql.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, `TRUNCATE targets, users CASCADE`); err != nil {
		t.Fatalf("truncate database: %v", err)
	}
	now := time.Now().UTC()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users (id, username, password_hash, status) VALUES ($1, 'owner', 'fixture-hash', 'ACTIVE')`, []any{testUserID}},
		{`INSERT INTO agents (id, owner_user_id, name, status) VALUES ($1, $2, 'agent', 'ACTIVE')`, []any{testAgentID, testUserID}},
		{`INSERT INTO tasks (id, agent_id, status, created_at, last_heartbeat_at) VALUES ($1, $2, 'ACTIVE', $3, $3)`, []any{testTaskID, testAgentID, now}},
		{`INSERT INTO targets (id, name, connector_type, connection_config, status) VALUES ($1, 'target', 'HTTP', '{"base_url":"https://target.example.test","allowed_http_methods":["POST"]}', 'ACTIVE')`, []any{testTargetID}},
		{`INSERT INTO credentials (id, target_id, alias, credential_type, status, vault_provider, vault_path) VALUES ($1, $2, 'default', 'API_KEY', 'ACTIVE', 'OPENBAO', 'kv/data/fixture')`, []any{testCredentialID, testTargetID}},
		{`UPDATE targets SET default_credential_id = $1 WHERE id = $2`, []any{testCredentialID, testTargetID}},
		{`INSERT INTO operation_sets (id,name,executor_type,status,created_by_user_id,updated_by_user_id) VALUES ($1,'fixture-http','HTTP','ACTIVE',$2,$2)`, []any{testOperationSet, testUserID}},
		{`INSERT INTO operations (id,operation_set_id,operation_key,status,created_by_user_id,updated_by_user_id) VALUES ($1,$2,'fixture_post','ACTIVE',$3,$3)`, []any{testOperationID, testOperationSet, testUserID}},
		{`INSERT INTO operation_versions (operation_id,version,name,operation_kind,risk_level,arguments_schema,execution_template,definition_hash,created_by_user_id) VALUES ($1,1,'Fixture POST','HTTP','MEDIUM','{"type":"object","properties":{},"required":[],"additionalProperties":false}','{"kind":"HTTP","http":{"method":"POST","path":"/execute"}}',$2,$3)`, []any{testOperationID, testDefinitionHash[:], testUserID}},
		{`UPDATE operations SET current_version=1 WHERE id=$1`, []any{testOperationID}},
		{`INSERT INTO target_operation_bindings (target_id,operation_id,version,status,created_by_user_id,updated_by_user_id) VALUES ($1,$2,1,'ACTIVE',$3,$3)`, []any{testTargetID, testOperationID, testUserID}},
		{`INSERT INTO authorization_requests (id,agent_id,task_id,target_id,credential_id,operation,parameters,operation_hash,reason,status,created_at,approval_deadline,request_format,operation_id,operation_version,arguments,definition_hash,target_config_version) VALUES ($1,$2,$3,$4,$5,'HTTP','{"kind":"HTTP","http":{"method":"POST","path":"/execute"}}',$6,'fixture reason','PENDING_APPROVAL',$7,$8,2,$9,1,'{}',$10,1)`, []any{testRequestID, testAgentID, testTaskID, testTargetID, testCredentialID, testOperationHash[:], now, now.Add(authorization.ApprovalWait), testOperationID, testDefinitionHash[:]}},
	}
	for _, statement := range statements {
		if _, err := database.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed query failed: %v", err)
		}
	}
}

func assertDecisionRows(t *testing.T, database *sql.DB) {
	t.Helper()
	var decision string
	var approvals, grants int
	if err := database.QueryRow(`SELECT status FROM authorization_requests WHERE id=$1`, testRequestID).Scan(&decision); err != nil {
		t.Fatalf("read decision: %v", err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM approvals WHERE request_id=$1`, testRequestID).Scan(&approvals); err != nil {
		t.Fatalf("count approvals: %v", err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM operation_grants WHERE request_id=$1`, testRequestID).Scan(&grants); err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if approvals != 1 || decision == "REJECTED" && grants != 0 || decision == "APPROVED" && grants != 1 {
		t.Fatalf("decision=%s approvals=%d grants=%d", decision, approvals, grants)
	}
}

func seedApprovedGrant(t *testing.T, database *sql.DB) {
	t.Helper()
	now := time.Now().UTC()
	insertSafeAuthorizationRequest(t, database, testClaimRequest, testTaskID, "APPROVED", now, now.Add(authorization.MaximumGrantTTL))
	_, err := database.Exec(`
INSERT INTO operation_grants (id, request_id, agent_id, task_id, target_id, credential_id, operation_hash, approved_at, expires_at, status)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'APPROVED')`,
		testGrantID, testClaimRequest, testAgentID, testTaskID, testTargetID,
		testCredentialID, testOperationHash[:], now, now.Add(authorization.MaximumGrantTTL),
	)
	if err != nil {
		t.Fatalf("seed approved grant: %v", err)
	}
}

func insertSafeAuthorizationRequest(t *testing.T, database *sql.DB, requestID, taskID, status string, createdAt, deadline time.Time) {
	t.Helper()
	_, err := database.Exec(`
INSERT INTO authorization_requests (id,agent_id,task_id,target_id,credential_id,operation,parameters,operation_hash,reason,status,created_at,approval_deadline,request_format,operation_id,operation_version,arguments,definition_hash,target_config_version)
VALUES ($1,$2,$3,$4,$5,'HTTP','{"kind":"HTTP","http":{"method":"POST","path":"/execute"}}',$6,'fixture',$7,$8,$9,2,$10,1,'{}',$11,1)`,
		requestID, testAgentID, taskID, testTargetID, testCredentialID, testOperationHash[:], status, createdAt, deadline, testOperationID, testDefinitionHash[:],
	)
	if err != nil {
		t.Fatalf("seed safe authorization request: %v", err)
	}
}
