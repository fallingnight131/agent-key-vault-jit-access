package store

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/audit"
	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/domain"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgreSQLAuditChainAndRetention(t *testing.T) {
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
	authorizationRepository := NewPostgreSQLAuthorizationRepository(database)
	claim := authorization.ClaimContext{GrantID: testGrantID, AgentID: testAgentID, TaskID: testTaskID, TargetID: testTargetID, CredentialID: testCredentialID, OperationHash: testOperationHash}
	grant, err := authorization.NewExecutionGuard(authorizationRepository).Claim(context.Background(), claim)
	if err != nil {
		t.Fatal(err)
	}
	executions := NewPostgreSQLExecutionRepository(database)
	executionID, err := executions.Start(context.Background(), grant, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := executions.Finish(context.Background(), executionID, domain.ExecutionSucceeded, time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}
	reclaimID, err := executions.StartReclaim(context.Background(), executionID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := executions.FinishReclaim(context.Background(), reclaimID, true, time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}
	var linked int
	if err := database.QueryRow(`SELECT count(*) FROM audit_events WHERE request_id=$1 AND grant_id=$2 AND execution_id=$3 AND reclaim_id=$4`, testClaimRequest, testGrantID, executionID, reclaimID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked < 1 {
		t.Fatal("audit chain has no fully linked reclaim event")
	}
	rows, err := database.Query(`SELECT metadata::text FROM audit_events WHERE request_id=$1`, testClaimRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var metadata string
		if err := rows.Scan(&metadata); err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(metadata)
		for _, forbidden := range []string{"password", "token", "authorization", "fixture-secret"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("unsafe audit metadata %q", metadata)
			}
		}
	}
	old := time.Now().UTC().Add(-audit.RetentionPeriod - time.Hour)
	if _, err := database.Exec(`INSERT INTO audit_events (id,event_type,actor_type,created_at) SELECT gen_random_uuid(),'retention.fixture','SYSTEM',$1 FROM generate_series(1,1001)`, old); err != nil {
		t.Fatal(err)
	}
	deleted, err := NewPostgreSQLAuditRepository(database).DeleteBefore(context.Background(), time.Now().UTC().Add(-audit.RetentionPeriod), audit.CleanupBatch)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != audit.CleanupBatch {
		t.Fatalf("deleted=%d", deleted)
	}
	var remaining int
	if err := database.QueryRow(`SELECT count(*) FROM audit_events WHERE event_type='retention.fixture'`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("remaining=%d", remaining)
	}
}
