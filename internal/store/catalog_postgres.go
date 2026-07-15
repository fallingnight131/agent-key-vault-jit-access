package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fallingnight/akv/internal/catalog"
)

type PostgreSQLCatalogRepository struct{ database *sql.DB }

func NewPostgreSQLCatalogRepository(database *sql.DB) *PostgreSQLCatalogRepository {
	return &PostgreSQLCatalogRepository{database: database}
}

func (repository *PostgreSQLCatalogRepository) ListCatalog(ctx context.Context) ([]catalog.Target, []catalog.Credential, error) {
	rows, err := repository.database.QueryContext(ctx, `SELECT id,name,description,connector_type,connection_config,default_credential_id,status,created_at FROM targets ORDER BY name,id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var targets []catalog.Target
	for rows.Next() {
		var target catalog.Target
		var connector, status string
		var configuration []byte
		if err := rows.Scan(&target.ID, &target.Name, &target.Description, &connector, &configuration, &target.DefaultCredentialID, &status, &target.CreatedAt); err != nil {
			return nil, nil, err
		}
		target.ConnectorType, target.Active = catalog.ConnectorType(connector), status == "ACTIVE"
		if err := json.Unmarshal(configuration, &target.ConnectionConfig); err != nil {
			return nil, nil, err
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	credentialRows, err := repository.database.QueryContext(ctx, `SELECT id,target_id,alias,credential_type,status,vault_provider,vault_path,vault_version,created_at FROM credentials ORDER BY created_at,id`)
	if err != nil {
		return nil, nil, err
	}
	defer credentialRows.Close()
	var credentials []catalog.Credential
	for credentialRows.Next() {
		var credential catalog.Credential
		var kind, status string
		if err := credentialRows.Scan(&credential.ID, &credential.TargetID, &credential.Alias, &kind, &status, &credential.VaultProvider, &credential.VaultPath, &credential.VaultVersion, &credential.CreatedAt); err != nil {
			return nil, nil, err
		}
		credential.Type, credential.Active = catalog.CredentialType(kind), status == "ACTIVE"
		credentials = append(credentials, credential)
	}
	return targets, credentials, credentialRows.Err()
}

func (repository *PostgreSQLCatalogRepository) FindCredential(ctx context.Context, credentialID string) (catalog.Credential, error) {
	var credential catalog.Credential
	var kind, status string
	err := repository.database.QueryRowContext(ctx, `SELECT id,target_id,alias,credential_type,status,vault_provider,vault_path,vault_version,created_at FROM credentials WHERE id=$1`, credentialID).Scan(&credential.ID, &credential.TargetID, &credential.Alias, &kind, &status, &credential.VaultProvider, &credential.VaultPath, &credential.VaultVersion, &credential.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.Credential{}, catalog.ErrUnavailable
	}
	credential.Type, credential.Active = catalog.CredentialType(kind), status == "ACTIVE"
	return credential, err
}

func (repository *PostgreSQLCatalogRepository) FindTargetWithDefaultCredential(ctx context.Context, targetID string) (catalog.Target, catalog.Credential, error) {
	var target catalog.Target
	var credential catalog.Credential
	var connector, targetStatus, kind, credentialStatus string
	var configuration []byte
	err := repository.database.QueryRowContext(ctx, `SELECT t.id,t.name,t.description,t.connector_type,t.connection_config,t.default_credential_id,t.status,t.created_at,c.id,c.target_id,c.alias,c.credential_type,c.status,c.vault_provider,c.vault_path,c.vault_version,c.created_at FROM targets t JOIN credentials c ON c.id=t.default_credential_id WHERE t.id=$1`, targetID).Scan(&target.ID, &target.Name, &target.Description, &connector, &configuration, &target.DefaultCredentialID, &targetStatus, &target.CreatedAt, &credential.ID, &credential.TargetID, &credential.Alias, &kind, &credentialStatus, &credential.VaultProvider, &credential.VaultPath, &credential.VaultVersion, &credential.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.Target{}, catalog.Credential{}, catalog.ErrUnavailable
	}
	if err != nil {
		return catalog.Target{}, catalog.Credential{}, err
	}
	target.ConnectorType, target.Active = catalog.ConnectorType(connector), targetStatus == "ACTIVE"
	credential.Type, credential.Active = catalog.CredentialType(kind), credentialStatus == "ACTIVE"
	if err := json.Unmarshal(configuration, &target.ConnectionConfig); err != nil {
		return catalog.Target{}, catalog.Credential{}, err
	}
	return target, credential, nil
}

func (repository *PostgreSQLCatalogRepository) UpdateTarget(ctx context.Context, target catalog.Target, at time.Time) error {
	configuration, err := json.Marshal(target.ConnectionConfig)
	if err != nil {
		return err
	}
	result, err := repository.database.ExecContext(ctx, `UPDATE targets SET description=$2,connection_config=$3,updated_at=$4 WHERE id=$1`, target.ID, target.Description, configuration, at)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return catalog.ErrUnavailable
	}
	return nil
}

func (repository *PostgreSQLCatalogRepository) SetTargetActive(ctx context.Context, targetID string, active bool, at time.Time) error {
	status := "DISABLED"
	if active {
		status = "ACTIVE"
	}
	result, err := repository.database.ExecContext(ctx, `UPDATE targets SET status=$2,updated_at=$3 WHERE id=$1`, targetID, status, at)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return catalog.ErrUnavailable
	}
	return nil
}

func (repository *PostgreSQLCatalogRepository) SetCredentialActive(ctx context.Context, credentialID string, active bool, at time.Time) error {
	status := "DISABLED"
	if active {
		status = "ACTIVE"
	}
	result, err := repository.database.ExecContext(ctx, `UPDATE credentials SET status=$2,updated_at=$3 WHERE id=$1`, credentialID, status, at)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return catalog.ErrUnavailable
	}
	return nil
}

func (repository *PostgreSQLCatalogRepository) CreateTargetWithDefaultCredential(ctx context.Context, target catalog.Target, credential catalog.Credential) error {
	configuration, err := json.Marshal(target.ConnectionConfig)
	if err != nil {
		return err
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	_, err = transaction.ExecContext(ctx, `INSERT INTO targets (id,name,description,connector_type,connection_config,status,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,'ACTIVE',$6,$6)`, target.ID, target.Name, target.Description, target.ConnectorType, configuration, target.CreatedAt)
	if err != nil {
		return err
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO credentials (id,target_id,alias,credential_type,status,vault_provider,vault_path,vault_version,created_at,updated_at) VALUES ($1,$2,$3,$4,'ACTIVE',$5,$6,$7,$8,$8)`, credential.ID, target.ID, credential.Alias, credential.Type, credential.VaultProvider, credential.VaultPath, credential.VaultVersion, credential.CreatedAt)
	if err != nil {
		return err
	}
	_, err = transaction.ExecContext(ctx, `UPDATE targets SET default_credential_id=$2 WHERE id=$1`, target.ID, credential.ID)
	if err != nil {
		return err
	}
	return transaction.Commit()
}

func (repository *PostgreSQLCatalogRepository) ListActiveTargets(ctx context.Context) ([]catalog.Target, error) {
	rows, err := repository.database.QueryContext(ctx, `SELECT id,name,description,connector_type,connection_config,default_credential_id,status,created_at FROM targets WHERE status='ACTIVE' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []catalog.Target
	for rows.Next() {
		var target catalog.Target
		var connector, status string
		var configuration []byte
		if err := rows.Scan(&target.ID, &target.Name, &target.Description, &connector, &configuration, &target.DefaultCredentialID, &status, &target.CreatedAt); err != nil {
			return nil, err
		}
		target.ConnectorType = catalog.ConnectorType(connector)
		target.Active = status == "ACTIVE"
		if err := json.Unmarshal(configuration, &target.ConnectionConfig); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (repository *PostgreSQLCatalogRepository) FindActiveTargetAndDefaultCredential(ctx context.Context, targetID string) (catalog.Target, catalog.Credential, error) {
	var target catalog.Target
	var credential catalog.Credential
	var connector, targetStatus, credentialType, credentialStatus string
	var configuration []byte
	err := repository.database.QueryRowContext(ctx, `SELECT t.id,t.name,t.description,t.connector_type,t.connection_config,t.default_credential_id,t.status,t.created_at,c.id,c.alias,c.credential_type,c.status,c.vault_provider,c.vault_path,c.vault_version,c.created_at FROM targets t JOIN credentials c ON c.id=t.default_credential_id WHERE t.id=$1 AND t.status='ACTIVE' AND c.status='ACTIVE'`, targetID).Scan(&target.ID, &target.Name, &target.Description, &connector, &configuration, &target.DefaultCredentialID, &targetStatus, &target.CreatedAt, &credential.ID, &credential.Alias, &credentialType, &credentialStatus, &credential.VaultProvider, &credential.VaultPath, &credential.VaultVersion, &credential.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.Target{}, catalog.Credential{}, catalog.ErrUnavailable
	}
	if err != nil {
		return catalog.Target{}, catalog.Credential{}, fmt.Errorf("find catalog target: %w", err)
	}
	target.ConnectorType = catalog.ConnectorType(connector)
	target.Active = true
	credential.Type = catalog.CredentialType(credentialType)
	credential.Active = true
	credential.TargetID = target.ID
	if err := json.Unmarshal(configuration, &target.ConnectionConfig); err != nil {
		return catalog.Target{}, catalog.Credential{}, err
	}
	return target, credential, nil
}
