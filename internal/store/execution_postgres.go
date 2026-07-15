package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/domain"
	"github.com/fallingnight/akv/internal/lifecycle"
	"github.com/fallingnight/akv/internal/proxy"
)

type PostgreSQLExecutionRepository struct {
	database *sql.DB
	newID    func() (string, error)
}

func NewPostgreSQLExecutionRepository(database *sql.DB) *PostgreSQLExecutionRepository {
	return &PostgreSQLExecutionRepository{database: database, newID: randomExecutionID}
}

func (repository *PostgreSQLExecutionRepository) LoadPlan(ctx context.Context, requestID string) (proxy.Plan, error) {
	var plan proxy.Plan
	var connectorType, credentialType string
	var targetStatus, credentialStatus string
	var configJSON, operationJSON []byte
	var operationHash []byte
	err := repository.database.QueryRowContext(ctx, `
SELECT r.id, g.id, r.agent_id, r.task_id, r.operation_hash, r.parameters,
       t.id, t.name, t.description, t.connector_type, t.connection_config,
       t.default_credential_id, t.status,
       c.id, c.alias, c.credential_type, c.status, c.vault_provider,
       c.vault_path, c.vault_version
FROM authorization_requests r
JOIN operation_grants g ON g.request_id = r.id
JOIN targets t ON t.id = r.target_id
JOIN credentials c ON c.id = r.credential_id
WHERE r.id = $1`, requestID).Scan(
		&plan.RequestID, &plan.GrantID, &plan.AgentID, &plan.TaskID,
		&operationHash, &operationJSON,
		&plan.Target.ID, &plan.Target.Name, &plan.Target.Description, &connectorType,
		&configJSON, &plan.Target.DefaultCredentialID, &targetStatus,
		&plan.Credential.ID, &plan.Credential.Alias, &credentialType,
		&credentialStatus, &plan.Credential.VaultProvider,
		&plan.Credential.VaultPath, &plan.Credential.VaultVersion,
	)
	if err != nil {
		return proxy.Plan{}, fmt.Errorf("load execution plan: %w", err)
	}
	if len(operationHash) != sha256.Size {
		return proxy.Plan{}, errors.New("invalid execution operation hash")
	}
	copy(plan.OperationHash[:], operationHash)
	plan.Target.ConnectorType = catalog.ConnectorType(connectorType)
	plan.Target.Active = targetStatus == "ACTIVE"
	plan.Credential.Type = catalog.CredentialType(credentialType)
	plan.Credential.TargetID = plan.Target.ID
	plan.Credential.Active = credentialStatus == "ACTIVE"
	if err := json.Unmarshal(configJSON, &plan.Target.ConnectionConfig); err != nil {
		return proxy.Plan{}, fmt.Errorf("decode target connection config: %w", err)
	}
	if err := json.Unmarshal(operationJSON, &plan.Operation); err != nil {
		return proxy.Plan{}, fmt.Errorf("decode frozen operation: %w", err)
	}
	return plan, nil
}

func (repository *PostgreSQLExecutionRepository) Start(ctx context.Context, grant authorization.Grant, startedAt time.Time) (string, error) {
	id, err := repository.newID()
	if err != nil {
		return "", fmt.Errorf("generate execution ID: %w", err)
	}
	result, err := repository.database.ExecContext(ctx, `
INSERT INTO executions (id, grant_id, status, started_at)
SELECT $1, id, 'EXECUTING', $3
FROM operation_grants
WHERE id = $2 AND status = 'EXECUTING' AND claimed_at IS NOT NULL`, id, grant.ID, startedAt)
	if err != nil {
		return "", fmt.Errorf("start execution: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return "", errors.New("execution grant is not claimed")
	}
	return id, nil
}

func (repository *PostgreSQLExecutionRepository) RecordLease(ctx context.Context, executionID, leaseID string) error {
	if leaseID == "" {
		return errors.New("empty lease ID")
	}
	result, err := repository.database.ExecContext(ctx, `UPDATE executions SET vault_lease_id=$2 WHERE id=$1 AND status='EXECUTING' AND vault_lease_id IS NULL`, executionID, leaseID)
	if err != nil {
		return fmt.Errorf("record execution lease: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return errors.New("execution lease cannot be recorded")
	}
	return nil
}

func (repository *PostgreSQLExecutionRepository) Finish(ctx context.Context, executionID string, status domain.ExecutionStatus, completedAt time.Time, errorCode string) error {
	if !domain.ExecutionExecuting.CanTransitionTo(status) {
		return errors.New("invalid execution terminal status")
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin execution finish: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	result, err := transaction.ExecContext(ctx, `
UPDATE executions
SET status = $2, completed_at = $3, error_code = NULLIF($4, '')
WHERE id = $1 AND status = 'EXECUTING'`, executionID, status, completedAt, errorCode)
	if err != nil {
		return fmt.Errorf("finish execution: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errors.New("execution is not active")
	}
	result, err = transaction.ExecContext(ctx, `
UPDATE operation_grants g
SET status = $2, completed_at = $3
FROM executions e
WHERE e.id = $1 AND g.id = e.grant_id AND g.status = 'EXECUTING'`, executionID, status, completedAt)
	if err != nil {
		return fmt.Errorf("finish operation grant: %w", err)
	}
	rows, err = result.RowsAffected()
	if err != nil || rows != 1 {
		return errors.New("operation grant is not executing")
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit execution finish: %w", err)
	}
	return nil
}

func (repository *PostgreSQLExecutionRepository) StartReclaim(ctx context.Context, executionID string, startedAt time.Time) (string, error) {
	id, err := repository.newID()
	if err != nil {
		return "", fmt.Errorf("generate reclaim ID: %w", err)
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin reclaim: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	result, err := transaction.ExecContext(ctx, `
UPDATE operation_grants g SET status='RECLAIMING'
FROM executions e
WHERE e.id=$1 AND g.id=e.grant_id
  AND e.status IN ('SUCCEEDED','FAILED','CANCELLED','TIMED_OUT')
  AND (g.status=e.status OR g.status='RECLAIM_FAILED')`, executionID)
	if err != nil {
		return "", fmt.Errorf("start grant reclaim: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return "", errors.New("execution is not ready for reclaim")
	}
	_, err = transaction.ExecContext(ctx, `
INSERT INTO reclaims (id, execution_id, status, started_at, attempt)
SELECT $1,$2,'RECLAIMING',$3,COALESCE(MAX(attempt),0)+1 FROM reclaims WHERE execution_id=$2`, id, executionID, startedAt)
	if err != nil {
		return "", fmt.Errorf("insert reclaim: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return "", fmt.Errorf("commit reclaim start: %w", err)
	}
	return id, nil
}

func (repository *PostgreSQLExecutionRepository) FinishReclaim(ctx context.Context, reclaimID string, success bool, completedAt time.Time, errorCode string) error {
	status := domain.ReclaimFailed
	grantStatus := domain.GrantReclaimFailed
	if success {
		status, grantStatus = domain.Reclaimed, domain.GrantReclaimed
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reclaim finish: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	result, err := transaction.ExecContext(ctx, `
UPDATE reclaims SET status=$2,completed_at=$3,error_code=NULLIF($4,'')
WHERE id=$1 AND status='RECLAIMING'`, reclaimID, status, completedAt, errorCode)
	if err != nil {
		return fmt.Errorf("finish reclaim: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return errors.New("reclaim is not active")
	}
	result, err = transaction.ExecContext(ctx, `
UPDATE operation_grants g SET status=$2
FROM reclaims r JOIN executions e ON e.id=r.execution_id
WHERE r.id=$1 AND g.id=e.grant_id AND g.status='RECLAIMING'`, reclaimID, grantStatus)
	if err != nil {
		return fmt.Errorf("finish grant reclaim: %w", err)
	}
	rows, _ = result.RowsAffected()
	if rows != 1 {
		return errors.New("grant is not reclaiming")
	}
	if !success {
		incidentID, err := repository.newID()
		if err != nil {
			return fmt.Errorf("generate incident ID: %w", err)
		}
		_, err = transaction.ExecContext(ctx, `INSERT INTO security_incidents (id,reclaim_id,status,error_code,created_at) VALUES ($1,$2,'OPEN',$3,$4)`, incidentID, reclaimID, errorCode, completedAt)
		if err != nil {
			return fmt.Errorf("create reclaim incident: %w", err)
		}
	} else {
		_, err = transaction.ExecContext(ctx, `
UPDATE security_incidents SET status='RESOLVED',resolved_at=$2
WHERE status='OPEN' AND reclaim_id IN (
    SELECT previous.id FROM reclaims current
    JOIN reclaims previous ON previous.execution_id=current.execution_id
    WHERE current.id=$1
)`, reclaimID, completedAt)
		if err != nil {
			return fmt.Errorf("resolve reclaim incidents: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit reclaim finish: %w", err)
	}
	return nil
}

func (repository *PostgreSQLExecutionRepository) ListCancellationRequested(ctx context.Context) ([]string, error) {
	rows, err := repository.database.QueryContext(ctx, `
SELECT e.id
FROM executions e JOIN operation_grants g ON g.id=e.grant_id
WHERE e.status='EXECUTING' AND g.status='EXECUTING' AND g.revoked_at IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("list cancellation requests: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (repository *PostgreSQLExecutionRepository) MarkStuckAndListRecovery(ctx context.Context, now time.Time, limit int) ([]lifecycle.RecoveryItem, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin execution recovery: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	_, err = transaction.ExecContext(ctx, `
WITH stuck AS (
    SELECT e.id
    FROM executions e
    JOIN operation_grants g ON g.id=e.grant_id
    JOIN authorization_requests r ON r.id=g.request_id
    WHERE e.status='EXECUTING' AND g.status='EXECUTING'
      AND e.started_at <= $1::timestamptz - CASE r.operation
        WHEN 'HTTP' THEN interval '30 seconds'
        WHEN 'POSTGRESQL_STATEMENT' THEN interval '60 seconds'
        WHEN 'POSTGRESQL_TRANSACTION' THEN interval '5 minutes'
        ELSE interval '30 seconds' END
    FOR UPDATE OF e,g SKIP LOCKED
)
UPDATE executions e SET status='TIMED_OUT',completed_at=$1,error_code='PROXY_RECOVERY_TIMEOUT'
FROM stuck WHERE e.id=stuck.id`, now)
	if err != nil {
		return nil, fmt.Errorf("mark stuck executions: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `
UPDATE operation_grants g SET status='TIMED_OUT',completed_at=$1
FROM executions e
WHERE e.grant_id=g.id AND e.status='TIMED_OUT' AND g.status='EXECUTING'`, now)
	if err != nil {
		return nil, fmt.Errorf("mark stuck grants: %w", err)
	}
	rows, err := transaction.QueryContext(ctx, `
SELECT e.id,e.vault_lease_id
FROM executions e JOIN operation_grants g ON g.id=e.grant_id
WHERE g.status IN ('SUCCEEDED','FAILED','CANCELLED','TIMED_OUT','RECLAIM_FAILED')
  AND NOT EXISTS (SELECT 1 FROM reclaims r WHERE r.execution_id=e.id AND r.status IN ('RECLAIMING','RECLAIMED'))
ORDER BY COALESCE(e.completed_at,e.started_at)
LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list recovery candidates: %w", err)
	}
	defer rows.Close()
	var items []lifecycle.RecoveryItem
	for rows.Next() {
		var item lifecycle.RecoveryItem
		var lease sql.NullString
		if err := rows.Scan(&item.ExecutionID, &lease); err != nil {
			return nil, err
		}
		if lease.Valid {
			item.LeaseID = lease.String
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := transaction.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

func randomExecutionID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
