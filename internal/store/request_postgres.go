package store

import (
	"context"
	"database/sql"

	"github.com/fallingnight/akv/internal/authorization"
)

type PostgreSQLRequestRepository struct{ database *sql.DB }

func NewPostgreSQLRequestRepository(database *sql.DB) *PostgreSQLRequestRepository {
	return &PostgreSQLRequestRepository{database: database}
}
func (repository *PostgreSQLRequestRepository) CreateRequest(ctx context.Context, request authorization.Request) error {
	_, err := repository.database.ExecContext(ctx, `INSERT INTO authorization_requests (id,agent_id,task_id,target_id,credential_id,operation,parameters,operation_hash,reason,status,created_at,approval_deadline) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'PENDING_APPROVAL',$10,$11)`, request.ID, request.AgentID, request.TaskID, request.TargetID, request.CredentialID, request.OperationKind, request.OperationSnapshot, request.OperationHash[:], request.Reason, request.CreatedAt, request.ApprovalDeadline)
	return err
}
