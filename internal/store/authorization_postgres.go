package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/domain"
)

type PostgreSQLAuthorizationRepository struct {
	database *sql.DB
}

func NewPostgreSQLAuthorizationRepository(database *sql.DB) *PostgreSQLAuthorizationRepository {
	return &PostgreSQLAuthorizationRepository{database: database}
}

func (repository *PostgreSQLAuthorizationRepository) FindDecisionContext(ctx context.Context, requestID string) (authorization.DecisionContext, error) {
	var result authorization.DecisionContext
	var operationHash []byte
	var status string
	err := repository.database.QueryRowContext(ctx, `
SELECT r.id, r.agent_id, r.task_id, r.target_id, r.credential_id,
       r.operation, r.parameters, r.operation_hash, r.reason, r.status,
       r.created_at, r.approval_deadline, a.owner_user_id
FROM authorization_requests r
JOIN agents a ON a.id = r.agent_id
WHERE r.id = $1`, requestID).Scan(
		&result.Request.ID, &result.Request.AgentID, &result.Request.TaskID,
		&result.Request.TargetID, &result.Request.CredentialID,
		&result.Request.OperationKind, &result.Request.OperationSnapshot,
		&operationHash, &result.Request.Reason, &status,
		&result.Request.CreatedAt, &result.Request.ApprovalDeadline,
		&result.AgentOwnerUserID,
	)
	if err != nil {
		return authorization.DecisionContext{}, fmt.Errorf("find decision context: %w", err)
	}
	if len(operationHash) != sha256.Size {
		return authorization.DecisionContext{}, errors.New("invalid operation hash length")
	}
	copy(result.Request.OperationHash[:], operationHash)
	result.Request.Status = domain.RequestStatus(status)
	return result, nil
}

func (repository *PostgreSQLAuthorizationRepository) DecidePending(ctx context.Context, command authorization.DecisionCommand) error {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin approval transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	nextStatus := domain.RequestRejected
	if command.Approval.Decision == authorization.DecisionApproved {
		nextStatus = domain.RequestApproved
	}
	result, err := transaction.ExecContext(ctx, `
UPDATE authorization_requests
SET status = $1
WHERE id = $2
  AND status = 'PENDING_APPROVAL'
  AND approval_deadline > $3
  AND operation_hash = $4`,
		nextStatus, command.Context.Request.ID, command.Now, command.Context.Request.OperationHash[:],
	)
	if err != nil {
		return fmt.Errorf("decide request: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil || rowsAffected != 1 {
		return authorization.ErrDecisionConflict
	}
	_, err = transaction.ExecContext(ctx, `
INSERT INTO approvals (id, request_id, approver_user_id, decision, decided_at, grant_expires_at)
VALUES ($1, $2, $3, $4, $5, $6)`,
		command.Approval.ID, command.Approval.RequestID, command.Approval.ApproverUserID,
		command.Approval.Decision, command.Approval.DecidedAt, command.Approval.GrantExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert approval: %w", err)
	}
	if command.Grant != nil {
		grant := command.Grant
		_, err = transaction.ExecContext(ctx, `
INSERT INTO operation_grants (
    id, request_id, agent_id, task_id, target_id, credential_id, operation_hash,
    approved_at, expires_at, status
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'APPROVED')`,
			grant.ID, grant.RequestID, grant.AgentID, grant.TaskID, grant.TargetID,
			grant.CredentialID, grant.OperationHash[:], grant.ApprovedAt, grant.ExpiresAt,
		)
		if err != nil {
			return fmt.Errorf("insert operation grant: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit approval transaction: %w", err)
	}
	return nil
}

func (repository *PostgreSQLAuthorizationRepository) ExpirePending(ctx context.Context, requestID string, now time.Time) error {
	result, err := repository.database.ExecContext(ctx, `
UPDATE authorization_requests
SET status = 'APPROVAL_EXPIRED'
WHERE id = $1 AND status = 'PENDING_APPROVAL' AND approval_deadline <= $2`, requestID, now)
	if err != nil {
		return fmt.Errorf("expire request: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return authorization.ErrDecisionConflict
	}
	return nil
}

func (repository *PostgreSQLAuthorizationRepository) ClaimApproved(ctx context.Context, claim authorization.ClaimContext, now time.Time) (authorization.Grant, error) {
	var grant authorization.Grant
	var operationHash []byte
	var status string
	err := repository.database.QueryRowContext(ctx, `
UPDATE operation_grants AS g
SET status = 'EXECUTING', claimed_at = $7
FROM tasks AS t
WHERE g.id = $1
  AND g.agent_id = $2
  AND g.task_id = $3
  AND g.target_id = $4
  AND g.credential_id = $5
  AND g.operation_hash = $6
  AND g.status = 'APPROVED'
  AND g.expires_at > $7
  AND t.id = g.task_id
  AND t.agent_id = g.agent_id
  AND t.status = 'ACTIVE'
RETURNING g.id, g.request_id, g.agent_id, g.task_id, g.target_id,
          g.credential_id, g.operation_hash, g.approved_at, g.expires_at,
          g.status, g.claimed_at, g.completed_at, g.revoked_at`,
		claim.GrantID, claim.AgentID, claim.TaskID, claim.TargetID,
		claim.CredentialID, claim.OperationHash[:], now,
	).Scan(
		&grant.ID, &grant.RequestID, &grant.AgentID, &grant.TaskID, &grant.TargetID,
		&grant.CredentialID, &operationHash, &grant.ApprovedAt, &grant.ExpiresAt,
		&status, &grant.ClaimedAt, &grant.CompletedAt, &grant.RevokedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return authorization.Grant{}, authorization.ErrClaimDenied
	}
	if err != nil {
		return authorization.Grant{}, fmt.Errorf("claim operation grant: %w", err)
	}
	if len(operationHash) != sha256.Size {
		return authorization.Grant{}, authorization.ErrClaimDenied
	}
	copy(grant.OperationHash[:], operationHash)
	grant.Status = domain.GrantStatus(status)
	return grant, nil
}
