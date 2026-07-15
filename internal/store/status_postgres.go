package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/fallingnight/akv/internal/control"
)

func (repository *PostgreSQLRequestRepository) GetAuthorizationStatus(ctx context.Context, agentID, requestID string) (control.AuthorizationStatus, error) {
	var result control.AuthorizationStatus
	var decision, grantStatus, executionStatus, reclaimStatus, errorCode sql.NullString
	var grantExpires sql.NullTime
	err := repository.database.QueryRowContext(ctx, `
SELECT r.id,r.status,r.approval_deadline,a.decision,g.status,g.expires_at,e.status,rc.status,COALESCE(rc.error_code,e.error_code)
FROM authorization_requests r
LEFT JOIN approvals a ON a.request_id=r.id
LEFT JOIN operation_grants g ON g.request_id=r.id
LEFT JOIN executions e ON e.grant_id=g.id
LEFT JOIN LATERAL (SELECT status,error_code FROM reclaims WHERE execution_id=e.id ORDER BY attempt DESC LIMIT 1) rc ON true
WHERE r.id=$1 AND r.agent_id=$2`, requestID, agentID).Scan(&result.RequestID, &result.RequestStatus, &result.ApprovalDeadline, &decision, &grantStatus, &grantExpires, &executionStatus, &reclaimStatus, &errorCode)
	if err != nil {
		return control.AuthorizationStatus{}, fmt.Errorf("read authorization status: %w", err)
	}
	if decision.Valid {
		result.Decision = &decision.String
	}
	if grantStatus.Valid {
		result.GrantStatus = &grantStatus.String
	}
	if grantExpires.Valid {
		result.GrantExpiresAt = &grantExpires.Time
	}
	if executionStatus.Valid {
		result.ExecutionStatus = &executionStatus.String
	}
	if reclaimStatus.Valid {
		result.ReclaimStatus = &reclaimStatus.String
	}
	if errorCode.Valid {
		result.ErrorCode = &errorCode.String
	}
	return result, nil
}
