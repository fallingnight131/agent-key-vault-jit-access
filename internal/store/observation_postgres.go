package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/fallingnight/akv/internal/domain"
	"github.com/fallingnight/akv/internal/identity"
	"github.com/fallingnight/akv/internal/observation"
	"github.com/jackc/pgx/v5/pgconn"
)

type PostgreSQLObservationRepository struct {
	database *sql.DB
}

func NewPostgreSQLObservationRepository(database *sql.DB) *PostgreSQLObservationRepository {
	return &PostgreSQLObservationRepository{database: database}
}

type requestObservationContext struct {
	status             domain.RequestStatus
	createdAt          time.Time
	approvalDeadline   time.Time
	databaseNow        time.Time
	captureActive      bool
	executionCompleted sql.NullTime
	reviewStartID      sql.NullString
	reviewStartActorID sql.NullString
	reviewStartedAt    sql.NullTime
	reviewCompletionID sql.NullString
	reviewCompletedAt  sql.NullTime
}

type observationQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (repository *PostgreSQLObservationRepository) Record(
	ctx context.Context,
	actor identity.User,
	event observation.Event,
) (observation.Event, bool, error) {
	if repository == nil || repository.database == nil || !validObservationEvent(actor, event) {
		return observation.Event{}, false, observation.ErrInvalidInput
	}

	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return observation.Event{}, false, fmt.Errorf("begin observation transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	if _, err := transaction.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, event.RequestID,
	); err != nil {
		return observation.Event{}, false, fmt.Errorf("lock observation request: %w", err)
	}

	requestContext, err := loadRequestObservationContext(ctx, transaction, actor, event.RequestID)
	if err != nil {
		return observation.Event{}, false, err
	}
	if !requestContext.captureActive {
		return observation.Event{}, false, observation.ErrUnavailable
	}

	existing, found, err := findIdempotentObservation(ctx, transaction, event)
	if err != nil {
		return observation.Event{}, false, err
	}
	if found {
		if !sameIdempotentObservation(existing, event) {
			return observation.Event{}, false, observation.ErrConflict
		}
		if err := transaction.Commit(); err != nil {
			return observation.Event{}, false, fmt.Errorf("commit observation retry: %w", err)
		}
		return existing, false, nil
	}

	if !canRecordObservation(requestContext, actor, event) {
		return observation.Event{}, false, observation.ErrConflict
	}

	event.OccurredAt = time.Time{}
	err = transaction.QueryRowContext(ctx, `
INSERT INTO request_observation_events (
    id, request_id, actor_user_id, event_type, review_session_id, idempotency_key
) VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid, $6)
RETURNING occurred_at`,
		event.ID, event.RequestID, event.ActorUserID, event.Type,
		event.ReviewSessionID, event.IdempotencyKey,
	).Scan(&event.OccurredAt)
	if err != nil {
		if isObservationConstraintError(err) {
			return observation.Event{}, false, observation.ErrConflict
		}
		return observation.Event{}, false, fmt.Errorf("insert request observation: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return observation.Event{}, false, fmt.Errorf("commit request observation: %w", err)
	}
	return event, true, nil
}

func (repository *PostgreSQLObservationRepository) Summarize(
	ctx context.Context,
	actor identity.User,
	requestID string,
) (observation.Summary, error) {
	if repository == nil || repository.database == nil || !actor.OwnerActive || actor.ID == "" || requestID == "" {
		return observation.Summary{}, observation.ErrInvalidInput
	}

	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return observation.Summary{}, fmt.Errorf("begin observation summary transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	requestContext, err := loadRequestObservationContext(ctx, transaction, actor, requestID)
	if err != nil {
		return observation.Summary{}, err
	}
	summary := observation.Summary{
		RequestID:     requestID,
		CaptureStatus: observation.CaptureUnknown,
		ReviewStatus:  observation.ReviewUnknown,
	}
	if requestContext.executionCompleted.Valid &&
		!requestContext.executionCompleted.Time.Before(requestContext.createdAt) {
		duration := requestContext.executionCompleted.Time.Sub(requestContext.createdAt).Milliseconds()
		summary.RequestToResultDurationMS = &duration
	}
	if !requestContext.captureActive {
		if err := transaction.Commit(); err != nil {
			return observation.Summary{}, fmt.Errorf("commit unknown observation summary: %w", err)
		}
		return summary, nil
	}

	var manualHandoffs, approvalFollowups int64
	if err := transaction.QueryRowContext(ctx, `
SELECT
    count(*) FILTER (WHERE event_type = 'MANUAL_HANDOFF'),
    count(*) FILTER (WHERE event_type = 'APPROVAL_FOLLOWUP')
FROM request_observation_events
WHERE request_id = $1`, requestID).Scan(&manualHandoffs, &approvalFollowups); err != nil {
		return observation.Summary{}, fmt.Errorf("count request observations: %w", err)
	}
	summary.CaptureStatus = observation.CaptureActive
	summary.ManualHandoffCount = &manualHandoffs
	summary.ApprovalFollowupCount = &approvalFollowups
	summary.ReviewStatus = observation.ReviewNotStarted
	summary.CanRecordManualHandoff = requestAllowsApprovalObservation(requestContext)
	summary.CanRecordApprovalFollowup = summary.CanRecordManualHandoff

	if requestContext.reviewStartID.Valid {
		reviewSessionID := requestContext.reviewStartID.String
		summary.ReviewSessionID = &reviewSessionID
		summary.ReviewStatus = observation.ReviewInProgress
		summary.CanCompleteReview = !requestContext.reviewCompletionID.Valid &&
			requestContext.reviewStartActorID.Valid &&
			requestContext.reviewStartActorID.String == actor.ID
	}
	if requestContext.reviewCompletionID.Valid {
		summary.ReviewStatus = observation.ReviewCompleted
		summary.CanCompleteReview = false
		if requestContext.reviewStartedAt.Valid && requestContext.reviewCompletedAt.Valid &&
			!requestContext.reviewCompletedAt.Time.Before(requestContext.reviewStartedAt.Time) {
			duration := requestContext.reviewCompletedAt.Time.Sub(requestContext.reviewStartedAt.Time).Milliseconds()
			summary.OperationReviewDurationMS = &duration
		}
	}
	summary.CanStartReview = requestContext.executionCompleted.Valid &&
		!requestContext.executionCompleted.Time.Before(requestContext.createdAt) &&
		!requestContext.reviewStartID.Valid

	if err := transaction.Commit(); err != nil {
		return observation.Summary{}, fmt.Errorf("commit observation summary: %w", err)
	}
	return summary, nil
}

func loadRequestObservationContext(
	ctx context.Context,
	queryer observationQueryer,
	actor identity.User,
	requestID string,
) (requestObservationContext, error) {
	var result requestObservationContext
	var status string
	err := queryer.QueryRowContext(ctx, `
SELECT
    request_record.status,
    request_record.created_at,
    request_record.approval_deadline,
    clock_timestamp(),
    capture.request_id IS NOT NULL,
    execution_record.completed_at,
    review_start.id,
    review_start.actor_user_id,
    review_start.occurred_at,
    review_completion.id,
    review_completion.occurred_at
FROM authorization_requests request_record
JOIN agents request_agent ON request_agent.id = request_record.agent_id
LEFT JOIN request_observation_capture capture
    ON capture.request_id = request_record.id
LEFT JOIN operation_grants grant_record
    ON grant_record.request_id = request_record.id
LEFT JOIN executions execution_record
    ON execution_record.grant_id = grant_record.id
LEFT JOIN request_observation_events review_start
    ON review_start.request_id = request_record.id
   AND review_start.event_type = 'REVIEW_STARTED'
LEFT JOIN request_observation_events review_completion
    ON review_completion.request_id = request_record.id
   AND review_completion.event_type = 'REVIEW_COMPLETED'
WHERE request_record.id = $1
  AND $2
  AND ($3 OR $4 OR request_agent.owner_user_id = $5)`,
		requestID, actor.OwnerActive, actor.IsAdmin, actor.ApproveAll, actor.ID,
	).Scan(
		&status, &result.createdAt, &result.approvalDeadline, &result.databaseNow,
		&result.captureActive, &result.executionCompleted,
		&result.reviewStartID, &result.reviewStartActorID, &result.reviewStartedAt,
		&result.reviewCompletionID, &result.reviewCompletedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return requestObservationContext{}, observation.ErrNotFound
	}
	if err != nil {
		return requestObservationContext{}, fmt.Errorf("load request observation context: %w", err)
	}
	result.status = domain.RequestStatus(status)
	return result, nil
}

func findIdempotentObservation(
	ctx context.Context,
	queryer observationQueryer,
	event observation.Event,
) (observation.Event, bool, error) {
	var existing observation.Event
	var reviewSessionID sql.NullString
	var eventType string
	err := queryer.QueryRowContext(ctx, `
SELECT id, request_id, actor_user_id, event_type, review_session_id, idempotency_key, occurred_at
FROM request_observation_events
WHERE request_id = $1 AND actor_user_id = $2 AND idempotency_key = $3`,
		event.RequestID, event.ActorUserID, event.IdempotencyKey,
	).Scan(
		&existing.ID, &existing.RequestID, &existing.ActorUserID, &eventType,
		&reviewSessionID, &existing.IdempotencyKey, &existing.OccurredAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return observation.Event{}, false, nil
	}
	if err != nil {
		return observation.Event{}, false, fmt.Errorf("find idempotent observation: %w", err)
	}
	existing.Type = observation.EventType(eventType)
	if reviewSessionID.Valid {
		existing.ReviewSessionID = reviewSessionID.String
	}
	return existing, true, nil
}

func sameIdempotentObservation(existing, requested observation.Event) bool {
	if existing.Type != requested.Type {
		return false
	}
	if requested.Type == observation.EventReviewStarted {
		return true
	}
	return existing.ReviewSessionID == requested.ReviewSessionID
}

func validObservationEvent(actor identity.User, event observation.Event) bool {
	if !actor.OwnerActive || actor.ID == "" || actor.ID != event.ActorUserID ||
		event.ID == "" || event.RequestID == "" || event.IdempotencyKey == "" {
		return false
	}
	switch event.Type {
	case observation.EventManualHandoff, observation.EventApprovalFollowup:
		return event.ReviewSessionID == ""
	case observation.EventReviewStarted:
		return event.ReviewSessionID == event.ID
	case observation.EventReviewCompleted:
		return event.ReviewSessionID != ""
	default:
		return false
	}
}

func canRecordObservation(
	requestContext requestObservationContext,
	actor identity.User,
	event observation.Event,
) bool {
	switch event.Type {
	case observation.EventManualHandoff, observation.EventApprovalFollowup:
		return requestAllowsApprovalObservation(requestContext)
	case observation.EventReviewStarted:
		return requestContext.executionCompleted.Valid &&
			!requestContext.executionCompleted.Time.Before(requestContext.createdAt) &&
			!requestContext.reviewStartID.Valid
	case observation.EventReviewCompleted:
		return requestContext.reviewStartID.Valid &&
			requestContext.reviewStartID.String == event.ReviewSessionID &&
			requestContext.reviewStartActorID.Valid &&
			requestContext.reviewStartActorID.String == actor.ID &&
			!requestContext.reviewCompletionID.Valid
	default:
		return false
	}
}

func requestAllowsApprovalObservation(requestContext requestObservationContext) bool {
	return requestContext.status == domain.RequestPendingApproval &&
		requestContext.databaseNow.Before(requestContext.approvalDeadline)
}

func isObservationConstraintError(err error) bool {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return false
	}
	switch postgresError.Code {
	case "23503", "23505", "23514", "55000":
		return true
	default:
		return false
	}
}
