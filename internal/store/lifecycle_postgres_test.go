package store

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/authorization"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgreSQLLifecycleSweepAndRevoke(t *testing.T) {
	dsn := os.Getenv("AKV_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AKV_TEST_POSTGRES_DSN is not set")
	}
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	seedAuthorizationDatabase(t, database)
	seedApprovedGrant(t, database)
	repository := NewPostgreSQLLifecycleRepository(database)

	revoked, err := repository.RevokeRequest(context.Background(), testClaimRequest, time.Now().UTC())
	if err != nil || !revoked.RevokedBeforeExecution {
		t.Fatalf("RevokeRequest() result=%+v error=%v", revoked, err)
	}
	guard := authorization.NewExecutionGuard(NewPostgreSQLAuthorizationRepository(database))
	if _, err := guard.Claim(context.Background(), authorization.ClaimContext{
		GrantID: testGrantID, AgentID: testAgentID, TaskID: testTaskID,
		TargetID: testTargetID, CredentialID: testCredentialID, OperationHash: testOperationHash,
	}); err == nil {
		t.Fatal("revoked grant was claimable")
	}

	now := time.Now().UTC()
	created := now.Add(-time.Hour)
	const expiredRequest = "00000000-0000-4000-8000-000000000020"
	const expiredGrantRequest = "00000000-0000-4000-8000-000000000021"
	const expiredGrant = "00000000-0000-4000-8000-000000000022"
	insertRequest := `INSERT INTO authorization_requests (id,agent_id,task_id,target_id,credential_id,operation,parameters,operation_hash,reason,status,created_at,approval_deadline) VALUES ($1,$2,$3,$4,$5,'HTTP','{}',$6,'fixture',$7,$8,$9)`
	if _, err := database.Exec(insertRequest, expiredRequest, testAgentID, testTaskID, testTargetID, testCredentialID, testOperationHash[:], "PENDING_APPROVAL", created, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(insertRequest, expiredGrantRequest, testAgentID, testTaskID, testTargetID, testCredentialID, testOperationHash[:], "APPROVED", created, created.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO operation_grants (id,request_id,agent_id,task_id,target_id,credential_id,operation_hash,approved_at,expires_at,status) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'APPROVED')`, expiredGrant, expiredGrantRequest, testAgentID, testTaskID, testTargetID, testCredentialID, testOperationHash[:], created, created.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE tasks SET last_heartbeat_at=$2 WHERE id=$1`, testTaskID, now.Add(-46*time.Second)); err != nil {
		t.Fatal(err)
	}

	result, err := repository.SweepExpiredAndLost(context.Background(), now, now.Add(-45*time.Second))
	if err != nil {
		t.Fatalf("SweepExpiredAndLost() error = %v", err)
	}
	if result.ExpiredRequests != 1 || result.ExpiredGrants != 1 || result.LostTasks != 1 {
		t.Fatalf("sweep result = %+v", result)
	}
	var requestStatus, grantStatus, taskStatus string
	if err := database.QueryRow(`SELECT (SELECT status FROM authorization_requests WHERE id=$1),(SELECT status FROM operation_grants WHERE id=$2),(SELECT status FROM tasks WHERE id=$3)`, expiredRequest, expiredGrant, testTaskID).Scan(&requestStatus, &grantStatus, &taskStatus); err != nil {
		t.Fatal(err)
	}
	if requestStatus != "APPROVAL_EXPIRED" || grantStatus != "GRANT_EXPIRED" || taskStatus != "AGENT_LOST" {
		t.Fatalf("request=%s grant=%s task=%s", requestStatus, grantStatus, taskStatus)
	}
}
