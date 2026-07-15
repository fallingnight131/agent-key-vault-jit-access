package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/fallingnight/akv/internal/agent"
)

type PostgreSQLAgentRepository struct{ database *sql.DB }

func NewPostgreSQLAgentRepository(database *sql.DB) *PostgreSQLAgentRepository {
	return &PostgreSQLAgentRepository{database: database}
}

func (repository *PostgreSQLAgentRepository) CreateAgentWithToken(ctx context.Context, record agent.Record, token agent.TokenRecord) error {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin agent registration: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	_, err = transaction.ExecContext(ctx, `INSERT INTO agents (id, owner_user_id, name, status, created_at, updated_at) VALUES ($1,$2,$3,'ACTIVE',$4,$4)`, record.ID, record.OwnerUserID, record.Name, record.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert agent: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO agent_tokens (id, agent_id, token_hash, expires_at, created_at) VALUES ($1,$2,$3,$4,$5)`, token.ID, record.ID, token.TokenHash[:], token.ExpiresAt, token.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert agent token: %w", err)
	}
	return transaction.Commit()
}

func (repository *PostgreSQLAgentRepository) ReplaceAgentToken(ctx context.Context, ownerID, agentID string, token agent.TokenRecord, at time.Time) error {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin token replacement: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	var ownedAgentID string
	if err := transaction.QueryRowContext(ctx, `SELECT id FROM agents WHERE id=$1 AND owner_user_id=$2 FOR UPDATE`, agentID, ownerID).Scan(&ownedAgentID); errors.Is(err, sql.ErrNoRows) {
		return agent.ErrForbidden
	} else if err != nil {
		return fmt.Errorf("lock agent for token replacement: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `UPDATE agent_tokens SET revoked_at=$2 WHERE agent_id=$1 AND revoked_at IS NULL`, agentID, at)
	if err != nil {
		return fmt.Errorf("revoke previous agent token: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO agent_tokens (id, agent_id, token_hash, expires_at, created_at) VALUES ($1,$2,$3,$4,$5)`, token.ID, agentID, token.TokenHash[:], token.ExpiresAt, token.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert replacement token: %w", err)
	}
	return transaction.Commit()
}

func (repository *PostgreSQLAgentRepository) FindAgentByTokenHash(ctx context.Context, hash [sha256.Size]byte) (agent.Record, agent.TokenRecord, error) {
	var record agent.Record
	var token agent.TokenRecord
	var status string
	var storedHash []byte
	err := repository.database.QueryRowContext(ctx, `
SELECT a.id,a.owner_user_id,a.name,a.status,a.created_at,
       t.id,t.agent_id,t.token_hash,t.created_at,t.expires_at,t.revoked_at
FROM agent_tokens t JOIN agents a ON a.id=t.agent_id
WHERE t.token_hash=$1`, hash[:]).Scan(
		&record.ID, &record.OwnerUserID, &record.Name, &status, &record.CreatedAt,
		&token.ID, &token.AgentID, &storedHash, &token.CreatedAt, &token.ExpiresAt, &token.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return agent.Record{}, agent.TokenRecord{}, agent.ErrUnauthorized
	}
	if err != nil {
		return agent.Record{}, agent.TokenRecord{}, fmt.Errorf("find agent token: %w", err)
	}
	if len(storedHash) != sha256.Size {
		return agent.Record{}, agent.TokenRecord{}, agent.ErrUnauthorized
	}
	copy(token.TokenHash[:], storedHash)
	record.Active = status == "ACTIVE"
	return record, token, nil
}

func (repository *PostgreSQLAgentRepository) RevokeAgentToken(ctx context.Context, ownerID, agentID string, at time.Time) error {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin token revocation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	var ownedAgentID string
	if err := transaction.QueryRowContext(ctx, `SELECT id FROM agents WHERE id=$1 AND owner_user_id=$2 FOR UPDATE`, agentID, ownerID).Scan(&ownedAgentID); errors.Is(err, sql.ErrNoRows) {
		return agent.ErrForbidden
	} else if err != nil {
		return fmt.Errorf("lock agent for token revocation: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE agent_tokens SET revoked_at=$2 WHERE agent_id=$1 AND revoked_at IS NULL`, ownedAgentID, at); err != nil {
		return fmt.Errorf("revoke agent token: %w", err)
	}
	return transaction.Commit()
}

func (repository *PostgreSQLAgentRepository) SetAgentActive(ctx context.Context, ownerID, agentID string, active bool, at time.Time) error {
	status := "DISABLED"
	if active {
		status = "ACTIVE"
	}
	result, err := repository.database.ExecContext(ctx, `UPDATE agents SET status=$3,updated_at=$4 WHERE id=$1 AND owner_user_id=$2`, agentID, ownerID, status, at)
	if err != nil {
		return fmt.Errorf("set agent active: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return agent.ErrForbidden
	}
	return nil
}

func (repository *PostgreSQLAgentRepository) ListOwnedAgents(ctx context.Context, ownerID string) ([]agent.View, error) {
	rows, err := repository.database.QueryContext(ctx, `SELECT a.id,a.name,a.status,a.created_at,t.id IS NOT NULL,t.expires_at FROM agents a LEFT JOIN agent_tokens t ON t.agent_id=a.id AND t.revoked_at IS NULL WHERE a.owner_user_id=$1 ORDER BY a.created_at,a.id`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []agent.View
	for rows.Next() {
		var record agent.View
		var status string
		var expiresAt sql.NullTime
		if err := rows.Scan(&record.ID, &record.Name, &status, &record.CreatedAt, &record.HasActiveToken, &expiresAt); err != nil {
			return nil, err
		}
		record.Active = status == "ACTIVE"
		if expiresAt.Valid {
			record.TokenExpiresAt = &expiresAt.Time
		}
		records = append(records, record)
	}
	return records, rows.Err()
}
