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
	var operationHash, definitionHash []byte
	var status string
	err := repository.database.QueryRowContext(ctx, `
SELECT r.id, r.agent_id, r.task_id, r.target_id, r.credential_id,
       r.operation, r.parameters, r.operation_hash, r.reason, r.status,
	       r.created_at, r.approval_deadline, a.owner_user_id,
	       r.request_format,COALESCE(r.operation_id::text,''),COALESCE(r.operation_version,0),
	       COALESCE(r.arguments,'null'::jsonb),COALESCE(r.definition_hash,''::bytea),
	       COALESCE(r.target_config_version,0)
FROM authorization_requests r
JOIN agents a ON a.id = r.agent_id
WHERE r.id = $1`, requestID).Scan(
		&result.Request.ID, &result.Request.AgentID, &result.Request.TaskID,
		&result.Request.TargetID, &result.Request.CredentialID,
		&result.Request.OperationKind, &result.Request.OperationSnapshot,
		&operationHash, &result.Request.Reason, &status,
		&result.Request.CreatedAt, &result.Request.ApprovalDeadline,
		&result.AgentOwnerUserID,
		&result.Request.RequestFormat, &result.Request.OperationID, &result.Request.OperationVersion,
		&result.Request.ArgumentsSnapshot, &definitionHash, &result.Request.TargetConfigVersion,
	)
	if err != nil {
		return authorization.DecisionContext{}, fmt.Errorf("find decision context: %w", err)
	}
	if len(operationHash) != sha256.Size {
		return authorization.DecisionContext{}, errors.New("invalid operation hash length")
	}
	copy(result.Request.OperationHash[:], operationHash)
	if result.Request.RequestFormat == 2 {
		if len(definitionHash) != sha256.Size {
			return authorization.DecisionContext{}, errors.New("invalid definition hash length")
		}
		copy(result.Request.DefinitionHash[:], definitionHash)
	}
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
	  AND operation_hash = $4
	  AND ($1 <> 'APPROVED' OR EXISTS (
	      SELECT 1
	      FROM targets target
	      JOIN credentials credential ON credential.id=authorization_requests.credential_id
	      WHERE target.id=authorization_requests.target_id
	        AND target.status='ACTIVE'
	        AND credential.status='ACTIVE'
	        AND target.default_credential_id=credential.id
	        AND authorization_requests.request_format=2
	        AND target.config_version=authorization_requests.target_config_version
	            AND EXISTS (
	                SELECT 1
	                FROM target_operation_bindings binding
	                JOIN operations operation_definition ON operation_definition.id=binding.operation_id
	                JOIN operation_sets operation_set ON operation_set.id=operation_definition.operation_set_id
	                JOIN operation_versions operation_version ON operation_version.operation_id=binding.operation_id
	                    AND operation_version.version=binding.version
	                WHERE binding.target_id=target.id
	                  AND binding.operation_id=authorization_requests.operation_id
	                  AND binding.version=authorization_requests.operation_version
	                  AND binding.status='ACTIVE'
	                  AND operation_definition.status='ACTIVE'
	                  AND operation_set.status='ACTIVE'
	                  AND operation_version.definition_hash=authorization_requests.definition_hash
	            )
	  ))`,
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
	FROM tasks AS t, authorization_requests AS r
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
	  AND r.id = g.request_id
	  AND r.operation_hash = g.operation_hash
	  AND EXISTS (
	      SELECT 1
	      FROM targets target
	      JOIN credentials credential ON credential.id=r.credential_id
	      WHERE target.id=r.target_id
	        AND target.status='ACTIVE'
	        AND credential.status='ACTIVE'
	        AND target.default_credential_id=credential.id
	        AND r.request_format=2
	        AND target.config_version=r.target_config_version
	            AND EXISTS (
	                SELECT 1
	                FROM target_operation_bindings binding
	                JOIN operations operation_definition ON operation_definition.id=binding.operation_id
	                JOIN operation_sets operation_set ON operation_set.id=operation_definition.operation_set_id
	                JOIN operation_versions operation_version ON operation_version.operation_id=binding.operation_id
	                    AND operation_version.version=binding.version
	                WHERE binding.target_id=target.id
	                  AND binding.operation_id=r.operation_id
	                  AND binding.version=r.operation_version
	                  AND binding.status='ACTIVE'
	                  AND operation_definition.status='ACTIVE'
	                  AND operation_set.status='ACTIVE'
	                  AND operation_version.definition_hash=r.definition_hash
	            )
	  )
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
		_, auditErr := repository.database.ExecContext(ctx, `
INSERT INTO audit_events (id,event_type,actor_type,actor_id,request_id,grant_id,metadata,created_at)
SELECT gen_random_uuid(),'operation_grants.claim_denied','AGENT',$2::uuid,g.request_id,g.id,
       '{"reason":"CONTEXT_OR_STATE_MISMATCH"}'::jsonb,$3
FROM operation_grants g WHERE g.id=$1`, claim.GrantID, claim.AgentID, now)
		if auditErr != nil {
			return authorization.Grant{}, fmt.Errorf("audit denied grant claim: %w", auditErr)
		}
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
