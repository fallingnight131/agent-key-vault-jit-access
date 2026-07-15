package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/fallingnight/akv/internal/domain"
	"github.com/fallingnight/akv/internal/task"
)

type PostgreSQLTaskRepository struct{ database *sql.DB }

func NewPostgreSQLTaskRepository(database *sql.DB) *PostgreSQLTaskRepository {
	return &PostgreSQLTaskRepository{database: database}
}

func (repository *PostgreSQLTaskRepository) CreateTask(ctx context.Context, record task.Record) error {
	_, err := repository.database.ExecContext(ctx, `INSERT INTO tasks (id,agent_id,status,created_at,last_heartbeat_at) VALUES ($1,$2,'ACTIVE',$3,$4)`, record.ID, record.AgentID, record.CreatedAt, record.LastHeartbeatAt)
	return err
}

func (repository *PostgreSQLTaskRepository) HeartbeatActiveTask(ctx context.Context, taskID, agentID string, at time.Time) error {
	result, err := repository.database.ExecContext(ctx, `UPDATE tasks SET last_heartbeat_at=$3 WHERE id=$1 AND agent_id=$2 AND status='ACTIVE'`, taskID, agentID, at)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return task.ErrTaskUnavailable
	}
	return nil
}

func (repository *PostgreSQLTaskRepository) EndActiveTask(ctx context.Context, taskID, agentID string, status domain.TaskStatus, at time.Time) ([]string, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = transaction.Rollback() }()
	result, err := transaction.ExecContext(ctx, `UPDATE tasks SET status=$3,ended_at=$4 WHERE id=$1 AND agent_id=$2 AND status='ACTIVE'`, taskID, agentID, status, at)
	if err != nil {
		return nil, err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return nil, task.ErrTaskUnavailable
	}
	_, err = transaction.ExecContext(ctx, `UPDATE operation_grants SET status='REVOKED',revoked_at=$2 WHERE task_id=$1 AND status='APPROVED'`, taskID, at)
	if err != nil {
		return nil, err
	}
	cancelRows, err := transaction.QueryContext(ctx, `UPDATE operation_grants g SET revoked_at=$2 WHERE g.task_id=$1 AND g.status='EXECUTING' AND g.revoked_at IS NULL RETURNING (SELECT e.id FROM executions e WHERE e.grant_id=g.id)`, taskID, at)
	if err != nil {
		return nil, err
	}
	defer cancelRows.Close()
	var executionIDs []string
	for cancelRows.Next() {
		var id sql.NullString
		if err := cancelRows.Scan(&id); err != nil {
			return nil, err
		}
		if id.Valid {
			executionIDs = append(executionIDs, id.String)
		}
	}
	if err := transaction.Commit(); err != nil {
		return nil, err
	}
	return executionIDs, nil
}

func (repository *PostgreSQLTaskRepository) FindActiveTask(ctx context.Context, taskID, agentID string) (task.Record, error) {
	var record task.Record
	var status string
	err := repository.database.QueryRowContext(ctx, `SELECT id,agent_id,status,created_at,last_heartbeat_at,ended_at FROM tasks WHERE id=$1 AND agent_id=$2 AND status='ACTIVE'`, taskID, agentID).Scan(&record.ID, &record.AgentID, &status, &record.CreatedAt, &record.LastHeartbeatAt, &record.EndedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return task.Record{}, task.ErrTaskUnavailable
	}
	if err != nil {
		return task.Record{}, err
	}
	record.Status = domain.TaskStatus(status)
	return record, nil
}

func (repository *PostgreSQLTaskRepository) MarkAgentLostBefore(ctx context.Context, cutoff, endedAt time.Time) ([]task.Record, error) {
	rows, err := repository.database.QueryContext(ctx, `UPDATE tasks SET status='AGENT_LOST',ended_at=$2 WHERE status='ACTIVE' AND last_heartbeat_at <= $1 RETURNING id,agent_id,status,created_at,last_heartbeat_at,ended_at`, cutoff, endedAt)
	if err != nil {
		return nil, fmt.Errorf("mark agent lost: %w", err)
	}
	defer rows.Close()
	var records []task.Record
	for rows.Next() {
		var record task.Record
		var status string
		if err := rows.Scan(&record.ID, &record.AgentID, &status, &record.CreatedAt, &record.LastHeartbeatAt, &record.EndedAt); err != nil {
			return nil, err
		}
		record.Status = domain.TaskStatus(status)
		records = append(records, record)
	}
	return records, rows.Err()
}
