package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/fallingnight/akv/internal/lifecycle"
)

type PostgreSQLLifecycleRepository struct {
	*PostgreSQLAuthorizationRepository
}

func NewPostgreSQLLifecycleRepository(database *sql.DB) *PostgreSQLLifecycleRepository {
	return &PostgreSQLLifecycleRepository{PostgreSQLAuthorizationRepository: NewPostgreSQLAuthorizationRepository(database)}
}

func (repository *PostgreSQLLifecycleRepository) RevokeRequest(ctx context.Context, requestID string, at time.Time) (lifecycle.RevokeResult, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return lifecycle.RevokeResult{}, fmt.Errorf("begin revoke: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	result, err := transaction.ExecContext(ctx, `
UPDATE operation_grants SET status='REVOKED',revoked_at=$2
WHERE request_id=$1 AND status='APPROVED' AND revoked_at IS NULL`, requestID, at)
	if err != nil {
		return lifecycle.RevokeResult{}, fmt.Errorf("revoke unclaimed grant: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 1 {
		if err := transaction.Commit(); err != nil {
			return lifecycle.RevokeResult{}, err
		}
		return lifecycle.RevokeResult{RevokedBeforeExecution: true}, nil
	}
	rowsResult, err := transaction.QueryContext(ctx, `
UPDATE operation_grants g SET revoked_at=$2
WHERE g.request_id=$1 AND g.status='EXECUTING' AND g.revoked_at IS NULL
RETURNING (SELECT e.id FROM executions e WHERE e.grant_id=g.id)`, requestID, at)
	if err != nil {
		return lifecycle.RevokeResult{}, fmt.Errorf("request executing cancellation: %w", err)
	}
	defer rowsResult.Close()
	var executionIDs []string
	for rowsResult.Next() {
		var executionID sql.NullString
		if err := rowsResult.Scan(&executionID); err != nil {
			return lifecycle.RevokeResult{}, err
		}
		if executionID.Valid {
			executionIDs = append(executionIDs, executionID.String)
		}
	}
	if len(executionIDs) == 0 {
		return lifecycle.RevokeResult{}, lifecycle.ErrRevokeUnavailable
	}
	if err := transaction.Commit(); err != nil {
		return lifecycle.RevokeResult{}, err
	}
	return lifecycle.RevokeResult{CancelExecutionIDs: executionIDs}, nil
}

func (repository *PostgreSQLLifecycleRepository) SweepExpiredAndLost(ctx context.Context, now, lostCutoff time.Time) (lifecycle.SweepResult, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return lifecycle.SweepResult{}, fmt.Errorf("begin lifecycle sweep: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	var result lifecycle.SweepResult
	queryCount := func(query string, arguments ...any) (int64, error) {
		outcome, err := transaction.ExecContext(ctx, query, arguments...)
		if err != nil {
			return 0, err
		}
		return outcome.RowsAffected()
	}
	result.ExpiredRequests, err = queryCount(`UPDATE authorization_requests SET status='APPROVAL_EXPIRED' WHERE status='PENDING_APPROVAL' AND approval_deadline <= $1`, now)
	if err != nil {
		return result, fmt.Errorf("expire requests: %w", err)
	}
	result.ExpiredGrants, err = queryCount(`UPDATE operation_grants SET status='GRANT_EXPIRED' WHERE status='APPROVED' AND expires_at <= $1`, now)
	if err != nil {
		return result, fmt.Errorf("expire grants: %w", err)
	}
	lostRows, err := transaction.QueryContext(ctx, `UPDATE tasks SET status='AGENT_LOST',ended_at=$1 WHERE status='ACTIVE' AND last_heartbeat_at <= $2 RETURNING id`, now, lostCutoff)
	if err != nil {
		return result, fmt.Errorf("end lost tasks: %w", err)
	}
	var lostTaskIDs []string
	for lostRows.Next() {
		var id string
		if err := lostRows.Scan(&id); err != nil {
			return result, err
		}
		lostTaskIDs = append(lostTaskIDs, id)
	}
	lostRows.Close()
	result.LostTasks = int64(len(lostTaskIDs))
	if len(lostTaskIDs) > 0 {
		_, err = transaction.ExecContext(ctx, `UPDATE operation_grants SET status='REVOKED',revoked_at=$1 WHERE status='APPROVED' AND task_id = ANY($2::uuid[])`, now, lostTaskIDs)
		if err != nil {
			return result, fmt.Errorf("revoke lost-task grants: %w", err)
		}
		cancelRows, err := transaction.QueryContext(ctx, `
UPDATE operation_grants g SET revoked_at=$1
WHERE g.status='EXECUTING' AND g.revoked_at IS NULL AND g.task_id = ANY($2::uuid[])
RETURNING (SELECT e.id FROM executions e WHERE e.grant_id=g.id)`, now, lostTaskIDs)
		if err != nil {
			return result, fmt.Errorf("cancel lost-task executions: %w", err)
		}
		for cancelRows.Next() {
			var id sql.NullString
			if err := cancelRows.Scan(&id); err != nil {
				return result, err
			}
			if id.Valid {
				result.CancelledExecutions = append(result.CancelledExecutions, id.String)
			}
		}
		cancelRows.Close()
	}
	if err := transaction.Commit(); err != nil {
		return result, fmt.Errorf("commit lifecycle sweep: %w", err)
	}
	return result, nil
}
