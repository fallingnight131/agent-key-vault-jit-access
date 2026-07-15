package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fallingnight/akv/internal/catalog"
)

func (repository *PostgreSQLCatalogRepository) ListOperationCatalog(ctx context.Context) (catalog.OperationCatalog, error) {
	var result catalog.OperationCatalog
	setRows, err := repository.database.QueryContext(ctx, `SELECT id,name,description,executor_type,status,created_by_user_id,updated_by_user_id,created_at,updated_at FROM operation_sets ORDER BY name,id`)
	if err != nil {
		return result, err
	}
	defer setRows.Close()
	for setRows.Next() {
		var item catalog.OperationSet
		var executorType, status string
		if err := setRows.Scan(&item.ID, &item.Name, &item.Description, &executorType, &status, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return result, err
		}
		item.ExecutorType, item.Active = catalog.ExecutorType(executorType), status == "ACTIVE"
		result.Sets = append(result.Sets, item)
	}
	if err := setRows.Err(); err != nil {
		return result, err
	}

	operationRows, err := repository.database.QueryContext(ctx, `SELECT id,operation_set_id,operation_key,current_version,status,created_by_user_id,updated_by_user_id,created_at,updated_at FROM operations ORDER BY operation_set_id,operation_key,id`)
	if err != nil {
		return result, err
	}
	defer operationRows.Close()
	for operationRows.Next() {
		var item catalog.SafeOperation
		var status string
		if err := operationRows.Scan(&item.ID, &item.OperationSetID, &item.Key, &item.CurrentVersion, &status, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return result, err
		}
		item.Active = status == "ACTIVE"
		result.Operations = append(result.Operations, item)
	}
	if err := operationRows.Err(); err != nil {
		return result, err
	}

	versionRows, err := repository.database.QueryContext(ctx, `SELECT operation_id,version,name,description,operation_kind,risk_level,arguments_schema,execution_template,definition_hash,created_by_user_id,created_at FROM operation_versions ORDER BY operation_id,version`)
	if err != nil {
		return result, err
	}
	defer versionRows.Close()
	for versionRows.Next() {
		item, err := scanOperationVersion(versionRows)
		if err != nil {
			return result, err
		}
		result.Versions = append(result.Versions, item)
	}
	if err := versionRows.Err(); err != nil {
		return result, err
	}

	bindingRows, err := repository.database.QueryContext(ctx, `SELECT target_id,operation_id,version,status,policy,created_by_user_id,updated_by_user_id,created_at,updated_at FROM target_operation_bindings ORDER BY target_id,operation_id`)
	if err != nil {
		return result, err
	}
	defer bindingRows.Close()
	for bindingRows.Next() {
		var item catalog.TargetOperationBinding
		var status string
		if err := bindingRows.Scan(&item.TargetID, &item.OperationID, &item.Version, &status, &item.Policy, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return result, err
		}
		item.Active = status == "ACTIVE"
		result.Bindings = append(result.Bindings, item)
	}
	return result, bindingRows.Err()
}

func (repository *PostgreSQLCatalogRepository) CreateOperationSet(ctx context.Context, set catalog.OperationSet) error {
	status := "DISABLED"
	if set.Active {
		status = "ACTIVE"
	}
	_, err := repository.database.ExecContext(ctx, `INSERT INTO operation_sets (id,name,description,executor_type,status,created_by_user_id,updated_by_user_id,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$6,$7,$7)`, set.ID, set.Name, set.Description, set.ExecutorType, status, set.CreatedBy, set.CreatedAt)
	return err
}

func (repository *PostgreSQLCatalogRepository) CreateOperationWithVersion(ctx context.Context, item catalog.SafeOperation, version catalog.OperationVersion) error {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	status := "DISABLED"
	if item.Active {
		status = "ACTIVE"
	}
	if _, err = transaction.ExecContext(ctx, `INSERT INTO operations (id,operation_set_id,operation_key,current_version,status,created_by_user_id,updated_by_user_id,created_at,updated_at) VALUES ($1,$2,$3,NULL,$4,$5,$5,$6,$6)`, item.ID, item.OperationSetID, item.Key, status, item.CreatedBy, item.CreatedAt); err != nil {
		return err
	}
	if err := insertOperationVersion(ctx, transaction, version); err != nil {
		return err
	}
	result, err := transaction.ExecContext(ctx, `UPDATE operations SET current_version=$2 WHERE id=$1 AND current_version IS NULL`, item.ID, version.Version)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return catalog.ErrUnavailable
	}
	return transaction.Commit()
}

func (repository *PostgreSQLCatalogRepository) PublishOperationVersion(ctx context.Context, version catalog.OperationVersion, expectedCurrent int, actorID string, at time.Time) error {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	if err := insertOperationVersion(ctx, transaction, version); err != nil {
		return err
	}
	result, err := transaction.ExecContext(ctx, `UPDATE operations SET current_version=$2,updated_by_user_id=$3,updated_at=$4 WHERE id=$1 AND current_version=$5`, version.OperationID, version.Version, actorID, at, expectedCurrent)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return catalog.ErrUnavailable
	}
	return transaction.Commit()
}

type operationVersionExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertOperationVersion(ctx context.Context, execer operationVersionExecer, version catalog.OperationVersion) error {
	_, err := execer.ExecContext(ctx, `INSERT INTO operation_versions (operation_id,version,name,description,operation_kind,risk_level,arguments_schema,execution_template,definition_hash,created_by_user_id,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, version.OperationID, version.Version, version.Name, version.Description, version.Kind, version.RiskLevel, []byte(version.ArgumentsSchema), []byte(version.ExecutionTemplate), version.DefinitionHash[:], version.CreatedBy, version.CreatedAt)
	return err
}

func (repository *PostgreSQLCatalogRepository) SetOperationSetActive(ctx context.Context, id string, active bool, actorID string, at time.Time) error {
	return repository.setOperationCatalogStatus(ctx, "operation_sets", id, active, actorID, at)
}

func (repository *PostgreSQLCatalogRepository) SetOperationActive(ctx context.Context, id string, active bool, actorID string, at time.Time) error {
	return repository.setOperationCatalogStatus(ctx, "operations", id, active, actorID, at)
}

func (repository *PostgreSQLCatalogRepository) setOperationCatalogStatus(ctx context.Context, table, id string, active bool, actorID string, at time.Time) error {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	status := "DISABLED"
	if active {
		status = "ACTIVE"
	}
	query := `UPDATE operation_sets SET status=$2,updated_by_user_id=$3,updated_at=$4 WHERE id=$1`
	if table == "operations" {
		query = `UPDATE operations SET status=$2,updated_by_user_id=$3,updated_at=$4 WHERE id=$1`
	}
	result, err := transaction.ExecContext(ctx, query, id, status, actorID, at)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return catalog.ErrUnavailable
	}
	if !active {
		revokeQuery := `
UPDATE operation_grants grant_record SET status='REVOKED',revoked_at=$2
FROM authorization_requests request_record,operations operation_definition
WHERE grant_record.request_id=request_record.id
  AND request_record.operation_id=operation_definition.id
  AND operation_definition.operation_set_id=$1
  AND request_record.request_format=2
  AND grant_record.status='APPROVED'`
		if table == "operations" {
			revokeQuery = `
UPDATE operation_grants grant_record SET status='REVOKED',revoked_at=$2
FROM authorization_requests request_record
WHERE grant_record.request_id=request_record.id
  AND request_record.operation_id=$1
  AND request_record.request_format=2
  AND grant_record.status='APPROVED'`
		}
		if _, err := transaction.ExecContext(ctx, revokeQuery, id, at); err != nil {
			return err
		}
	}
	return transaction.Commit()
}

func (repository *PostgreSQLCatalogRepository) UpsertTargetOperationBinding(ctx context.Context, binding catalog.TargetOperationBinding) error {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	status := "DISABLED"
	if binding.Active {
		status = "ACTIVE"
	}
	result, err := transaction.ExecContext(ctx, `
INSERT INTO target_operation_bindings (target_id,operation_id,version,status,policy,created_by_user_id,updated_by_user_id,created_at,updated_at)
SELECT t.id,o.id,v.version,$4,$5,$6,$6,$7,$7
FROM targets t
JOIN operations o ON o.id=$2
JOIN operation_sets s ON s.id=o.operation_set_id
JOIN operation_versions v ON v.operation_id=o.id AND v.version=$3
JOIN credentials c ON c.id=t.default_credential_id
WHERE t.id=$1
  AND ((s.executor_type='HTTP' AND t.connector_type='HTTP' AND c.credential_type IN ('API_KEY','ACCESS_TOKEN','USERNAME_PASSWORD'))
    OR (s.executor_type='POSTGRESQL' AND t.connector_type='POSTGRESQL' AND c.credential_type IN ('USERNAME_PASSWORD','POSTGRESQL_DYNAMIC'))
    OR (s.executor_type='SIGN' AND c.credential_type='TRANSIT_KEY'))
ON CONFLICT (target_id,operation_id) DO UPDATE
SET version=EXCLUDED.version,status=EXCLUDED.status,policy=EXCLUDED.policy,
    updated_by_user_id=EXCLUDED.updated_by_user_id,updated_at=EXCLUDED.updated_at`, binding.TargetID, binding.OperationID, binding.Version, status, []byte(binding.Policy), binding.UpdatedBy, binding.UpdatedAt)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return catalog.ErrUnavailable
	}
	if _, err := transaction.ExecContext(ctx, `
UPDATE operation_grants grant_record SET status='REVOKED',revoked_at=$4
FROM authorization_requests request_record
WHERE grant_record.request_id=request_record.id
  AND request_record.request_format=2
  AND request_record.target_id=$1
  AND request_record.operation_id=$2
  AND grant_record.status='APPROVED'
  AND ($3 <> request_record.operation_version OR $5 <> 'ACTIVE')`, binding.TargetID, binding.OperationID, binding.Version, binding.UpdatedAt, status); err != nil {
		return err
	}
	return transaction.Commit()
}

func (repository *PostgreSQLCatalogRepository) FindOperationVersion(ctx context.Context, operationID string, version int) (catalog.OperationSet, catalog.SafeOperation, catalog.OperationVersion, error) {
	var set catalog.OperationSet
	var item catalog.SafeOperation
	var versionRecord catalog.OperationVersion
	var setStatus, operationStatus string
	var hash []byte
	err := repository.database.QueryRowContext(ctx, `
SELECT s.id,s.name,s.description,s.executor_type,s.status,s.created_by_user_id,s.updated_by_user_id,s.created_at,s.updated_at,
       o.id,o.operation_set_id,o.operation_key,o.current_version,o.status,o.created_by_user_id,o.updated_by_user_id,o.created_at,o.updated_at,
       v.operation_id,v.version,v.name,v.description,v.operation_kind,v.risk_level,v.arguments_schema,v.execution_template,v.definition_hash,v.created_by_user_id,v.created_at
FROM operations o
JOIN operation_sets s ON s.id=o.operation_set_id
JOIN operation_versions v ON v.operation_id=o.id AND v.version=$2
WHERE o.id=$1`, operationID, version).Scan(
		&set.ID, &set.Name, &set.Description, &set.ExecutorType, &setStatus, &set.CreatedBy, &set.UpdatedBy, &set.CreatedAt, &set.UpdatedAt,
		&item.ID, &item.OperationSetID, &item.Key, &item.CurrentVersion, &operationStatus, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt,
		&versionRecord.OperationID, &versionRecord.Version, &versionRecord.Name, &versionRecord.Description, &versionRecord.Kind, &versionRecord.RiskLevel, &versionRecord.ArgumentsSchema, &versionRecord.ExecutionTemplate, &hash, &versionRecord.CreatedBy, &versionRecord.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return set, item, versionRecord, catalog.ErrUnavailable
	}
	if err != nil {
		return set, item, versionRecord, err
	}
	if len(hash) != sha256.Size {
		return set, item, versionRecord, catalog.ErrUnavailable
	}
	copy(versionRecord.DefinitionHash[:], hash)
	set.Active, item.Active = setStatus == "ACTIVE", operationStatus == "ACTIVE"
	return set, item, versionRecord, nil
}

func (repository *PostgreSQLCatalogRepository) ListActiveTargetOperations(ctx context.Context, targetID string) ([]catalog.PublicOperation, error) {
	rows, err := repository.database.QueryContext(ctx, `
SELECT o.id,b.version,t.id,o.operation_key,v.name,v.description,v.operation_kind,v.risk_level,v.arguments_schema
FROM target_operation_bindings b
JOIN targets t ON t.id=b.target_id AND t.status='ACTIVE'
JOIN credentials c ON c.id=t.default_credential_id AND c.status='ACTIVE'
JOIN operations o ON o.id=b.operation_id AND o.status='ACTIVE'
JOIN operation_sets s ON s.id=o.operation_set_id AND s.status='ACTIVE'
JOIN operation_versions v ON v.operation_id=b.operation_id AND v.version=b.version
WHERE b.target_id=$1 AND b.status='ACTIVE'
ORDER BY o.operation_key,o.id`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []catalog.PublicOperation
	for rows.Next() {
		var item catalog.PublicOperation
		if err := rows.Scan(&item.OperationID, &item.Version, &item.TargetID, &item.Key, &item.Name, &item.Description, &item.Kind, &item.RiskLevel, &item.ArgumentsSchema); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (repository *PostgreSQLCatalogRepository) FindActiveOperationForRequest(ctx context.Context, targetID, operationID string, version int) (catalog.ResolvedOperation, error) {
	var result catalog.ResolvedOperation
	var connectorType, targetStatus, credentialType, credentialStatus, setStatus, operationStatus string
	var configJSON, definitionHash []byte
	err := repository.database.QueryRowContext(ctx, `
SELECT t.id,t.name,t.description,t.connector_type,t.connection_config,t.config_version,t.default_credential_id,t.status,t.created_at,
       c.id,c.target_id,c.alias,c.credential_type,c.status,c.vault_provider,c.vault_path,c.vault_version,c.created_at,
       s.id,s.name,s.description,s.executor_type,s.status,s.created_by_user_id,s.updated_by_user_id,s.created_at,s.updated_at,
       o.id,o.operation_set_id,o.operation_key,o.current_version,o.status,o.created_by_user_id,o.updated_by_user_id,o.created_at,o.updated_at,
       v.operation_id,v.version,v.name,v.description,v.operation_kind,v.risk_level,v.arguments_schema,v.execution_template,v.definition_hash,v.created_by_user_id,v.created_at
FROM target_operation_bindings b
JOIN targets t ON t.id=b.target_id AND t.status='ACTIVE'
JOIN credentials c ON c.id=t.default_credential_id AND c.status='ACTIVE'
JOIN operations o ON o.id=b.operation_id AND o.status='ACTIVE'
JOIN operation_sets s ON s.id=o.operation_set_id AND s.status='ACTIVE'
JOIN operation_versions v ON v.operation_id=b.operation_id AND v.version=b.version
WHERE b.target_id=$1 AND b.operation_id=$2 AND b.version=$3 AND b.status='ACTIVE'`, targetID, operationID, version).Scan(
		&result.Target.ID, &result.Target.Name, &result.Target.Description, &connectorType, &configJSON, &result.Target.ConfigVersion, &result.Target.DefaultCredentialID, &targetStatus, &result.Target.CreatedAt,
		&result.Credential.ID, &result.Credential.TargetID, &result.Credential.Alias, &credentialType, &credentialStatus, &result.Credential.VaultProvider, &result.Credential.VaultPath, &result.Credential.VaultVersion, &result.Credential.CreatedAt,
		&result.Set.ID, &result.Set.Name, &result.Set.Description, &result.Set.ExecutorType, &setStatus, &result.Set.CreatedBy, &result.Set.UpdatedBy, &result.Set.CreatedAt, &result.Set.UpdatedAt,
		&result.Operation.ID, &result.Operation.OperationSetID, &result.Operation.Key, &result.Operation.CurrentVersion, &operationStatus, &result.Operation.CreatedBy, &result.Operation.UpdatedBy, &result.Operation.CreatedAt, &result.Operation.UpdatedAt,
		&result.Version.OperationID, &result.Version.Version, &result.Version.Name, &result.Version.Description, &result.Version.Kind, &result.Version.RiskLevel, &result.Version.ArgumentsSchema, &result.Version.ExecutionTemplate, &definitionHash, &result.Version.CreatedBy, &result.Version.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.ResolvedOperation{}, catalog.ErrUnavailable
	}
	if err != nil {
		return catalog.ResolvedOperation{}, fmt.Errorf("find active target operation: %w", err)
	}
	if len(definitionHash) != sha256.Size || json.Unmarshal(configJSON, &result.Target.ConnectionConfig) != nil {
		return catalog.ResolvedOperation{}, catalog.ErrUnavailable
	}
	copy(result.Version.DefinitionHash[:], definitionHash)
	result.Target.ConnectorType, result.Target.Active = catalog.ConnectorType(connectorType), targetStatus == "ACTIVE"
	result.Credential.Type, result.Credential.Active = catalog.CredentialType(credentialType), credentialStatus == "ACTIVE"
	result.Set.Active, result.Operation.Active = setStatus == "ACTIVE", operationStatus == "ACTIVE"
	return result, nil
}

type operationVersionScanner interface {
	Scan(...any) error
}

func scanOperationVersion(scanner operationVersionScanner) (catalog.OperationVersion, error) {
	var item catalog.OperationVersion
	var hash []byte
	if err := scanner.Scan(&item.OperationID, &item.Version, &item.Name, &item.Description, &item.Kind, &item.RiskLevel, &item.ArgumentsSchema, &item.ExecutionTemplate, &hash, &item.CreatedBy, &item.CreatedAt); err != nil {
		return item, err
	}
	if len(hash) != sha256.Size {
		return item, errors.New("invalid operation definition hash")
	}
	copy(item.DefinitionHash[:], hash)
	return item, nil
}
