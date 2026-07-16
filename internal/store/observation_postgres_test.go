package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/identity"
	"github.com/fallingnight/akv/internal/observation"
)

const (
	observationRequestID       = "00000000-0000-4000-8000-000000000201"
	observationExpiredRequest  = "00000000-0000-4000-8000-000000000202"
	observationOtherRequest    = "00000000-0000-4000-8000-000000000203"
	observationGrantID         = "00000000-0000-4000-8000-000000000204"
	observationExecutionID     = "00000000-0000-4000-8000-000000000205"
	observationUnrelatedUserID = "00000000-0000-4000-8000-000000000206"
	observationManualEventID   = "00000000-0000-4000-8000-000000000211"
	observationFollowupEventID = "00000000-0000-4000-8000-000000000212"
	observationReviewID        = "00000000-0000-4000-8000-000000000213"
	observationOrphanReviewID  = "00000000-0000-4000-8000-000000000214"
	observationManualKey       = "00000000-0000-4000-8000-000000000221"
	observationFollowupKey     = "00000000-0000-4000-8000-000000000222"
	observationReviewStartKey  = "00000000-0000-4000-8000-000000000223"
	observationReviewFinishKey = "00000000-0000-4000-8000-000000000224"
)

func TestPostgreSQLObservationCaptureVisibilityStateAndImmutability(t *testing.T) {
	database := openPostgreSQLObservationTest(t)
	defer database.Close()
	seedAuthorizationDatabase(t, database)
	ctx := context.Background()
	repository := NewPostgreSQLObservationRepository(database)
	owner := identity.User{ID: testUserID, OwnerActive: true}

	// A request that predates the capture migration has no enrollment. Its
	// absent events are unknown, not observed zeroes.
	if _, err := database.ExecContext(ctx, `TRUNCATE request_observation_capture CASCADE`); err != nil {
		t.Fatalf("remove observation enrollment to model a historical request: %v", err)
	}
	historical, err := repository.Summarize(ctx, owner, testRequestID)
	if err != nil {
		t.Fatalf("Summarize(historical) error = %v", err)
	}
	if historical.CaptureStatus != observation.CaptureUnknown ||
		historical.ManualHandoffCount != nil || historical.ApprovalFollowupCount != nil ||
		historical.OperationReviewDurationMS != nil || historical.ReviewStatus != observation.ReviewUnknown ||
		historical.CanRecordManualHandoff || historical.CanRecordApprovalFollowup ||
		historical.CanStartReview || historical.CanCompleteReview {
		t.Fatalf("historical summary = %+v, want unknown observation values", historical)
	}

	now := time.Now().UTC()
	insertSafeAuthorizationRequest(t, database, observationRequestID, testTaskID, "PENDING_APPROVAL", now, now.Add(time.Hour))
	initial, err := repository.Summarize(ctx, owner, observationRequestID)
	if err != nil {
		t.Fatalf("Summarize(new request) error = %v", err)
	}
	if initial.CaptureStatus != observation.CaptureActive ||
		initial.ManualHandoffCount == nil || *initial.ManualHandoffCount != 0 ||
		initial.ApprovalFollowupCount == nil || *initial.ApprovalFollowupCount != 0 ||
		initial.ReviewStatus != observation.ReviewNotStarted || initial.RequestToResultDurationMS != nil ||
		!initial.CanRecordManualHandoff || !initial.CanRecordApprovalFollowup ||
		initial.CanStartReview || initial.CanCompleteReview || initial.ImprovementTargetPercent != nil {
		t.Fatalf("new request summary = %+v, want active capture with observed zero counts", initial)
	}

	if _, err := database.ExecContext(ctx, `
INSERT INTO users (id, username, password_hash, status)
VALUES ($1, 'observation-unrelated', 'fixture-hash', 'ACTIVE')`, observationUnrelatedUserID); err != nil {
		t.Fatalf("seed unrelated observation user: %v", err)
	}
	unrelated := identity.User{ID: observationUnrelatedUserID, OwnerActive: true}
	if _, err := repository.Summarize(ctx, unrelated, observationRequestID); !errors.Is(err, observation.ErrNotFound) {
		t.Fatalf("unrelated Summarize() error = %v, want not found", err)
	}
	unauthorizedEvent := observation.Event{
		ID: observationManualEventID, RequestID: observationRequestID,
		ActorUserID: observationUnrelatedUserID, Type: observation.EventManualHandoff,
		IdempotencyKey: observationManualKey,
	}
	if _, _, err := repository.Record(ctx, unrelated, unauthorizedEvent); !errors.Is(err, observation.ErrNotFound) {
		t.Fatalf("unrelated Record() error = %v, want not found", err)
	}
	if summary, err := repository.Summarize(ctx, identity.User{
		ID: observationUnrelatedUserID, ApproveAll: true, OwnerActive: true,
	}, observationRequestID); err != nil || summary.CaptureStatus != observation.CaptureActive {
		t.Fatalf("APPROVE_ALL summary=%+v error=%v", summary, err)
	}

	manual := observation.Event{
		ID: observationManualEventID, RequestID: observationRequestID, ActorUserID: testUserID,
		Type: observation.EventManualHandoff, IdempotencyKey: observationManualKey,
		OccurredAt: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	recordedManual, created, err := repository.Record(ctx, owner, manual)
	if err != nil || !created {
		t.Fatalf("Record(manual) event=%+v created=%t error=%v", recordedManual, created, err)
	}
	if recordedManual.OccurredAt.IsZero() || recordedManual.OccurredAt.Equal(manual.OccurredAt) {
		t.Fatalf("manual occurred_at=%v, want database-assigned current time", recordedManual.OccurredAt)
	}
	retriedManual, created, err := repository.Record(ctx, owner, observation.Event{
		ID: "00000000-0000-4000-8000-000000000231", RequestID: observationRequestID,
		ActorUserID: testUserID, Type: observation.EventManualHandoff, IdempotencyKey: observationManualKey,
	})
	if err != nil || created || retriedManual.ID != recordedManual.ID ||
		!retriedManual.OccurredAt.Equal(recordedManual.OccurredAt) {
		t.Fatalf("idempotent manual event=%+v created=%t error=%v", retriedManual, created, err)
	}

	followup := observation.Event{
		ID: observationFollowupEventID, RequestID: observationRequestID, ActorUserID: testUserID,
		Type: observation.EventApprovalFollowup, IdempotencyKey: observationFollowupKey,
	}
	if _, created, err := repository.Record(ctx, owner, followup); err != nil || !created {
		t.Fatalf("Record(followup) created=%t error=%v", created, err)
	}

	expiredCreatedAt := now.Add(-2 * time.Hour)
	insertSafeAuthorizationRequest(t, database, observationExpiredRequest, testTaskID, "PENDING_APPROVAL", expiredCreatedAt, expiredCreatedAt.Add(time.Hour))
	if _, _, err := repository.Record(ctx, owner, observation.Event{
		ID: "00000000-0000-4000-8000-000000000232", RequestID: observationExpiredRequest,
		ActorUserID: testUserID, Type: observation.EventApprovalFollowup,
		IdempotencyKey: "00000000-0000-4000-8000-000000000233",
	}); !errors.Is(err, observation.ErrConflict) {
		t.Fatalf("expired approval followup error = %v, want conflict", err)
	}

	if _, err := database.ExecContext(ctx, `
UPDATE authorization_requests SET status = 'REJECTED' WHERE id = $1`, observationRequestID); err != nil {
		t.Fatalf("reject observed request: %v", err)
	}
	// Idempotency is checked before current-state eligibility so a response
	// retry remains stable after another actor decides the request.
	if retry, created, err := repository.Record(ctx, owner, manual); err != nil || created || retry.ID != recordedManual.ID {
		t.Fatalf("terminal-state retry event=%+v created=%t error=%v", retry, created, err)
	}
	if _, _, err := repository.Record(ctx, owner, observation.Event{
		ID: "00000000-0000-4000-8000-000000000234", RequestID: observationRequestID,
		ActorUserID: testUserID, Type: observation.EventManualHandoff,
		IdempotencyKey: "00000000-0000-4000-8000-000000000235",
	}); !errors.Is(err, observation.ErrConflict) {
		t.Fatalf("new terminal-state handoff error = %v, want conflict", err)
	}

	terminal, err := repository.Summarize(ctx, owner, observationRequestID)
	if err != nil || terminal.ManualHandoffCount == nil || *terminal.ManualHandoffCount != 1 ||
		terminal.ApprovalFollowupCount == nil || *terminal.ApprovalFollowupCount != 1 ||
		terminal.CanRecordManualHandoff || terminal.CanRecordApprovalFollowup {
		t.Fatalf("terminal summary=%+v error=%v", terminal, err)
	}

	for name, statement := range map[string]string{
		"update event":   `UPDATE request_observation_events SET occurred_at = clock_timestamp() WHERE id = '` + observationManualEventID + `'`,
		"delete event":   `DELETE FROM request_observation_events WHERE id = '` + observationManualEventID + `'`,
		"update capture": `UPDATE request_observation_capture SET started_at = clock_timestamp() WHERE request_id = '` + observationRequestID + `'`,
		"delete capture": `DELETE FROM request_observation_capture WHERE request_id = '` + observationRequestID + `'`,
	} {
		if _, err := database.ExecContext(ctx, statement); err == nil {
			t.Fatalf("%s succeeded for append-only observation data", name)
		}
	}
}

func TestPostgreSQLObservationReviewPairingAndConcurrency(t *testing.T) {
	database := openPostgreSQLObservationTest(t)
	defer database.Close()
	seedAuthorizationDatabase(t, database)
	ctx := context.Background()
	repository := NewPostgreSQLObservationRepository(database)
	owner := identity.User{ID: testUserID, OwnerActive: true}

	startEvent := observation.Event{
		ID: observationReviewID, RequestID: testRequestID, ActorUserID: testUserID,
		Type: observation.EventReviewStarted, ReviewSessionID: observationReviewID,
		IdempotencyKey: observationReviewStartKey,
	}
	if _, _, err := repository.Record(ctx, owner, startEvent); !errors.Is(err, observation.ErrConflict) {
		t.Fatalf("pre-execution review start error = %v, want conflict", err)
	}

	var requestCreatedAt time.Time
	if err := database.QueryRowContext(ctx, `
SELECT created_at FROM authorization_requests WHERE id = $1`, testRequestID).Scan(&requestCreatedAt); err != nil {
		t.Fatalf("read observed request creation: %v", err)
	}
	completedAt := time.Now().UTC()
	if completedAt.Before(requestCreatedAt) {
		completedAt = requestCreatedAt
	}
	if _, err := database.ExecContext(ctx, `
UPDATE authorization_requests SET status = 'APPROVED' WHERE id = $1`, testRequestID); err != nil {
		t.Fatalf("approve observed request: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO operation_grants (
    id, request_id, agent_id, task_id, target_id, credential_id, operation_hash,
    approved_at, expires_at, status, claimed_at, completed_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'SUCCEEDED',$8,$9)`,
		observationGrantID, testRequestID, testAgentID, testTaskID, testTargetID,
		testCredentialID, testOperationHash[:], requestCreatedAt, completedAt,
	); err != nil {
		t.Fatalf("seed completed observed grant: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO executions (id, grant_id, status, started_at, completed_at)
VALUES ($1,$2,'SUCCEEDED',$3,$4)`,
		observationExecutionID, observationGrantID, requestCreatedAt, completedAt,
	); err != nil {
		t.Fatalf("seed completed observed execution: %v", err)
	}
	var storedCompletedAt time.Time
	if err := database.QueryRowContext(ctx, `
SELECT completed_at FROM executions WHERE id = $1`, observationExecutionID).Scan(&storedCompletedAt); err != nil {
		t.Fatalf("read completed observed execution: %v", err)
	}
	expectedRequestDurationMS := storedCompletedAt.Sub(requestCreatedAt).Milliseconds()

	recordedStart, created, err := repository.Record(ctx, owner, startEvent)
	if err != nil || !created || recordedStart.ReviewSessionID != observationReviewID {
		t.Fatalf("Record(review start) event=%+v created=%t error=%v", recordedStart, created, err)
	}
	inProgress, err := repository.Summarize(ctx, owner, testRequestID)
	if err != nil || inProgress.ReviewStatus != observation.ReviewInProgress ||
		inProgress.ReviewSessionID == nil || *inProgress.ReviewSessionID != observationReviewID ||
		!inProgress.CanCompleteReview || inProgress.CanStartReview ||
		inProgress.RequestToResultDurationMS == nil ||
		*inProgress.RequestToResultDurationMS != expectedRequestDurationMS {
		t.Fatalf("in-progress review summary=%+v error=%v", inProgress, err)
	}
	if _, _, err := repository.Record(ctx, owner, observation.Event{
		ID: "00000000-0000-4000-8000-000000000241", RequestID: testRequestID,
		ActorUserID: testUserID, Type: observation.EventReviewStarted,
		ReviewSessionID: "00000000-0000-4000-8000-000000000241",
		IdempotencyKey:  "00000000-0000-4000-8000-000000000242",
	}); !errors.Is(err, observation.ErrConflict) {
		t.Fatalf("second review start error = %v, want conflict", err)
	}

	if _, err := database.ExecContext(ctx, `
INSERT INTO users (id, username, password_hash, status)
VALUES ($1, 'observation-reviewer', 'fixture-hash', 'ACTIVE')`, observationUnrelatedUserID); err != nil {
		t.Fatalf("seed alternate reviewer: %v", err)
	}
	otherReviewer := identity.User{ID: observationUnrelatedUserID, ApproveAll: true, OwnerActive: true}
	if _, _, err := repository.Record(ctx, otherReviewer, observation.Event{
		ID: "00000000-0000-4000-8000-000000000243", RequestID: testRequestID,
		ActorUserID: observationUnrelatedUserID, Type: observation.EventReviewCompleted,
		ReviewSessionID: observationReviewID,
		IdempotencyKey:  "00000000-0000-4000-8000-000000000244",
	}); !errors.Is(err, observation.ErrConflict) {
		t.Fatalf("cross-reviewer completion error = %v, want conflict", err)
	}

	insertSafeAuthorizationRequest(t, database, observationOtherRequest, testTaskID, "PENDING_APPROVAL", completedAt, completedAt.Add(time.Hour))
	if _, _, err := repository.Record(ctx, owner, observation.Event{
		ID: "00000000-0000-4000-8000-000000000245", RequestID: observationOtherRequest,
		ActorUserID: testUserID, Type: observation.EventReviewCompleted,
		ReviewSessionID: observationReviewID,
		IdempotencyKey:  "00000000-0000-4000-8000-000000000246",
	}); !errors.Is(err, observation.ErrConflict) {
		t.Fatalf("cross-request completion error = %v, want conflict", err)
	}

	time.Sleep(5 * time.Millisecond)
	const contenders = 12
	type completionResult struct {
		event   observation.Event
		created bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan completionResult, contenders)
	var wait sync.WaitGroup
	for contender := range contenders {
		wait.Add(1)
		go func(contender int) {
			defer wait.Done()
			<-start
			idempotencyKey := observationReviewFinishKey
			if contender > 0 {
				idempotencyKey = fmt.Sprintf("00000000-0000-4000-8000-%012d", 600+contender)
			}
			event, created, err := repository.Record(ctx, owner, observation.Event{
				ID:        fmt.Sprintf("00000000-0000-4000-8000-%012d", 500+contender),
				RequestID: testRequestID, ActorUserID: testUserID,
				Type: observation.EventReviewCompleted, ReviewSessionID: observationReviewID,
				IdempotencyKey: idempotencyKey,
			})
			results <- completionResult{event: event, created: created, err: err}
		}(contender)
	}
	close(start)
	wait.Wait()
	close(results)
	createdCount := 0
	conflictCount := 0
	var winner observation.Event
	for result := range results {
		switch {
		case result.err == nil && result.created:
			createdCount++
			winner = result.event
		case errors.Is(result.err, observation.ErrConflict) && !result.created:
			conflictCount++
		default:
			t.Fatalf("concurrent completion event=%+v created=%t error=%v", result.event, result.created, result.err)
		}
	}
	if createdCount != 1 || conflictCount != contenders-1 || winner.ID == "" {
		t.Fatalf("concurrent review created=%d conflicts=%d winner=%q", createdCount, conflictCount, winner.ID)
	}
	var completions int
	if err := database.QueryRowContext(ctx, `
SELECT count(*) FROM request_observation_events
WHERE request_id = $1 AND event_type = 'REVIEW_COMPLETED'`, testRequestID).Scan(&completions); err != nil {
		t.Fatalf("count review completions: %v", err)
	}
	if completions != 1 {
		t.Fatalf("review completions=%d, want 1", completions)
	}

	completed, err := repository.Summarize(ctx, owner, testRequestID)
	expectedReviewDurationMS := winner.OccurredAt.Sub(recordedStart.OccurredAt).Milliseconds()
	if err != nil || completed.ReviewStatus != observation.ReviewCompleted ||
		completed.OperationReviewDurationMS == nil ||
		*completed.OperationReviewDurationMS != expectedReviewDurationMS ||
		completed.CanCompleteReview || completed.CanStartReview ||
		completed.ImprovementTargetPercent != nil {
		t.Fatalf("completed review summary=%+v error=%v", completed, err)
	}
	if _, _, err := repository.Record(ctx, owner, observation.Event{
		ID: "00000000-0000-4000-8000-000000000247", RequestID: testRequestID,
		ActorUserID: testUserID, Type: observation.EventReviewCompleted,
		ReviewSessionID: observationReviewID,
		IdempotencyKey:  "00000000-0000-4000-8000-000000000248",
	}); !errors.Is(err, observation.ErrConflict) {
		t.Fatalf("second completion with a new key error = %v, want conflict", err)
	}

	if _, err := database.ExecContext(ctx, `
INSERT INTO request_observation_events (
    id, request_id, actor_user_id, event_type, review_session_id, idempotency_key
) VALUES ($1,$2,$3,'REVIEW_COMPLETED',$4,$5)`,
		observationOrphanReviewID, testRequestID, testUserID,
		"00000000-0000-4000-8000-000000000249",
		"00000000-0000-4000-8000-000000000250",
	); err == nil {
		t.Fatal("database accepted a review completion without a matching start")
	}
}

func openPostgreSQLObservationTest(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("AKV_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AKV_TEST_POSTGRES_DSN is not set")
	}
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if err := database.PingContext(context.Background()); err != nil {
		database.Close()
		t.Fatalf("PingContext() error = %v", err)
	}
	return database
}
