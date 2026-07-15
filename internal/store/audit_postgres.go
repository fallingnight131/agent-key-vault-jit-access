package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fallingnight/akv/internal/audit"
)

type PostgreSQLAuditRepository struct{ database *sql.DB }

func NewPostgreSQLAuditRepository(database *sql.DB) *PostgreSQLAuditRepository {
	return &PostgreSQLAuditRepository{database: database}
}

func (repository *PostgreSQLAuditRepository) Append(ctx context.Context, event audit.Event) error {
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return err
	}
	_, err = repository.database.ExecContext(ctx, `INSERT INTO audit_events (id,event_type,actor_type,actor_id,request_id,approval_id,grant_id,execution_id,reclaim_id,metadata,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, event.ID, event.Type, event.ActorType, event.ActorID, event.RequestID, event.ApprovalID, event.GrantID, event.ExecutionID, event.ReclaimID, metadata, event.CreatedAt)
	return err
}

func (repository *PostgreSQLAuditRepository) DeleteBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	result, err := repository.database.ExecContext(ctx, `DELETE FROM audit_events WHERE id IN (SELECT id FROM audit_events WHERE created_at < $1 ORDER BY created_at LIMIT $2)`, before, limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired audit events: %w", err)
	}
	return result.RowsAffected()
}
