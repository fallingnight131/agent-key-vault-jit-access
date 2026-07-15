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
	result, err := repository.database.ExecContext(ctx, `
INSERT INTO authorization_requests (
    id,agent_id,task_id,target_id,credential_id,operation,parameters,operation_hash,
    reason,status,created_at,approval_deadline,request_format,operation_id,
    operation_version,arguments,definition_hash,target_config_version
)
SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,'PENDING_APPROVAL',$10,$11,2,$12,$13,$14,$15,$16
FROM tasks task
JOIN targets target ON target.id=$4 AND target.status='ACTIVE' AND target.config_version=$16
JOIN credentials credential ON credential.id=$5 AND credential.target_id=target.id
    AND credential.status='ACTIVE' AND target.default_credential_id=credential.id
JOIN target_operation_bindings binding ON binding.target_id=target.id
    AND binding.operation_id=$12 AND binding.version=$13 AND binding.status='ACTIVE'
JOIN operations operation_definition ON operation_definition.id=binding.operation_id AND operation_definition.status='ACTIVE'
JOIN operation_sets operation_set ON operation_set.id=operation_definition.operation_set_id AND operation_set.status='ACTIVE'
JOIN operation_versions operation_version ON operation_version.operation_id=binding.operation_id
    AND operation_version.version=binding.version AND operation_version.definition_hash=$15
    AND operation_version.operation_kind=$6
WHERE task.id=$3 AND task.agent_id=$2 AND task.status='ACTIVE'`,
		request.ID, request.AgentID, request.TaskID, request.TargetID, request.CredentialID,
		request.OperationKind, request.OperationSnapshot, request.OperationHash[:], request.Reason,
		request.CreatedAt, request.ApprovalDeadline, request.OperationID, request.OperationVersion,
		request.ArgumentsSnapshot, request.DefinitionHash[:], request.TargetConfigVersion,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return authorization.ErrContextDenied
	}
	return nil
}
