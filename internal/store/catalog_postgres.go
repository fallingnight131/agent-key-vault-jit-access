package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fallingnight/akv/internal/catalog"
)

type PostgreSQLCatalogRepository struct{ database *sql.DB }

func NewPostgreSQLCatalogRepository(database *sql.DB) *PostgreSQLCatalogRepository {
	return &PostgreSQLCatalogRepository{database: database}
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
