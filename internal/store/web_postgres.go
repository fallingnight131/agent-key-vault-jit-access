package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fallingnight/akv/internal/control"
	"github.com/fallingnight/akv/internal/identity"
)

type PostgreSQLWebRepository struct{ database *sql.DB }

func NewPostgreSQLWebRepository(database *sql.DB) *PostgreSQLWebRepository {
	return &PostgreSQLWebRepository{database: database}
}

func (repository *PostgreSQLWebRepository) ListAuthorizationRequests(ctx context.Context, actor identity.User) ([]control.ApprovalView, error) {
	rows, err := repository.database.QueryContext(ctx, `SELECT r.id,r.agent_id,a.name,u.username,r.task_id,r.target_id,t.name,c.alias,c.credential_type,COALESCE(r.operation_id::text,''),COALESCE(r.operation_version,0),COALESCE(o.operation_key,''),COALESCE(v.name,''),r.operation,COALESCE(r.arguments,'null'::jsonb),r.parameters,r.reason,r.status,r.created_at,r.approval_deadline,g.expires_at,t.connector_type,COALESCE(v.risk_level,'') FROM authorization_requests r JOIN agents a ON a.id=r.agent_id JOIN users u ON u.id=a.owner_user_id JOIN targets t ON t.id=r.target_id JOIN credentials c ON c.id=r.credential_id LEFT JOIN operations o ON o.id=r.operation_id LEFT JOIN operation_versions v ON v.operation_id=r.operation_id AND v.version=r.operation_version LEFT JOIN operation_grants g ON g.request_id=r.id WHERE ($2 OR $3 OR a.owner_user_id=$1) ORDER BY (r.status='PENDING_APPROVAL') DESC,r.created_at DESC`, actor.ID, actor.IsAdmin, actor.ApproveAll)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []control.ApprovalView
	for rows.Next() {
		var record control.ApprovalView
		var arguments, operation []byte
		var expires sql.NullTime
		var connector string
		if err := rows.Scan(&record.RequestID, &record.AgentID, &record.AgentName, &record.OwnerUsername, &record.TaskID, &record.TargetID, &record.TargetName, &record.CredentialAlias, &record.CredentialType, &record.OperationID, &record.OperationVersion, &record.OperationKey, &record.OperationName, &record.OperationKind, &arguments, &operation, &record.Reason, &record.Status, &record.CreatedAt, &record.ApprovalDeadline, &expires, &connector, &record.RiskLevel); err != nil {
			return nil, err
		}
		record.Operation = append(json.RawMessage(nil), operation...)
		if record.OperationID != "" {
			record.Arguments = append(json.RawMessage(nil), arguments...)
		}
		if expires.Valid {
			record.GrantExpiresAt = &expires.Time
		}
		record.RiskHint = riskHint(connector, record.OperationKind)
		if record.RiskLevel != "" {
			record.RiskHint = record.RiskLevel + ": " + record.RiskHint
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (repository *PostgreSQLWebRepository) ReadAuthorizationAudit(ctx context.Context, actor identity.User, requestID string) ([]control.AuditView, error) {
	var allowed bool
	err := repository.database.QueryRowContext(ctx, `SELECT ($2 OR $3 OR a.owner_user_id=$1) FROM authorization_requests r JOIN agents a ON a.id=r.agent_id WHERE r.id=$4`, actor.ID, actor.IsAdmin, actor.ApproveAll, requestID).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) || !allowed {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	rows, err := repository.database.QueryContext(ctx, `SELECT id,event_type,actor_type,actor_id,request_id,approval_id,grant_id,execution_id,reclaim_id,metadata,created_at FROM audit_events WHERE request_id=$1 ORDER BY created_at,id`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []control.AuditView
	for rows.Next() {
		var event control.AuditView
		var metadata []byte
		if err := rows.Scan(&event.ID, &event.EventType, &event.ActorType, &event.ActorID, &event.RequestID, &event.ApprovalID, &event.GrantID, &event.ExecutionID, &event.ReclaimID, &metadata, &event.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(metadata, &event.Metadata); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (repository *PostgreSQLWebRepository) ListAuditEvents(ctx context.Context, actor identity.User) ([]control.AuditView, error) {
	if !actor.CanManageUsersAndTargets() {
		return nil, fmt.Errorf("forbidden")
	}
	rows, err := repository.database.QueryContext(ctx, `SELECT id,event_type,actor_type,actor_id,request_id,approval_id,grant_id,execution_id,reclaim_id,metadata,created_at FROM audit_events ORDER BY created_at DESC,id DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []control.AuditView
	for rows.Next() {
		var event control.AuditView
		var metadata []byte
		if err := rows.Scan(&event.ID, &event.EventType, &event.ActorType, &event.ActorID, &event.RequestID, &event.ApprovalID, &event.GrantID, &event.ExecutionID, &event.ReclaimID, &metadata, &event.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(metadata, &event.Metadata); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (repository *PostgreSQLWebRepository) ListSecurityIncidents(ctx context.Context, actor identity.User) ([]control.IncidentView, error) {
	if !actor.CanManageUsersAndTargets() {
		return nil, fmt.Errorf("forbidden")
	}
	rows, err := repository.database.QueryContext(ctx, `SELECT id,reclaim_id,status,error_code,created_at,resolved_at FROM security_incidents ORDER BY (status='OPEN') DESC,created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var incidents []control.IncidentView
	for rows.Next() {
		var incident control.IncidentView
		if err := rows.Scan(&incident.ID, &incident.ReclaimID, &incident.Status, &incident.ErrorCode, &incident.CreatedAt, &incident.ResolvedAt); err != nil {
			return nil, err
		}
		incidents = append(incidents, incident)
	}
	return incidents, rows.Err()
}

func riskHint(connector, operation string) string {
	switch connector {
	case "POSTGRESQL":
		return "Database statements may modify persistent data"
	case "HTTP":
		return "HTTP operation may change the target system"
	default:
		return "Review the exact frozen operation before approval"
	}
}

func (repository *PostgreSQLWebRepository) ResolveSecurityIncident(ctx context.Context, actor identity.User, incidentID string) error {
	if !actor.CanManageUsersAndTargets() || incidentID == "" {
		return fmt.Errorf("forbidden")
	}
	result, err := repository.database.ExecContext(ctx, `UPDATE security_incidents SET status='RESOLVED',resolved_at=now() WHERE id=$1 AND status='OPEN'`, incidentID)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return fmt.Errorf("incident unavailable")
	}
	return nil
}
