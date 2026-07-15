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
  AND g.status=e.status`, executionID)
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
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit reclaim finish: %w", err)
	}
	return nil
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
