package observation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fallingnight/akv/internal/identity"
)

var (
	ErrInvalidInput = errors.New("invalid observation input")
	ErrNotFound     = errors.New("observation request not found")
	ErrUnavailable  = errors.New("observation capture unavailable")
	ErrConflict     = errors.New("observation conflict")
)

type EventType string

const (
	EventManualHandoff    EventType = "MANUAL_HANDOFF"
	EventApprovalFollowup EventType = "APPROVAL_FOLLOWUP"
	EventReviewStarted    EventType = "REVIEW_STARTED"
	EventReviewCompleted  EventType = "REVIEW_COMPLETED"
)

const (
	CaptureUnknown = "UNKNOWN"
	CaptureActive  = "ACTIVE"

	ReviewUnknown    = "UNKNOWN"
	ReviewNotStarted = "NOT_STARTED"
	ReviewInProgress = "IN_PROGRESS"
	ReviewCompleted  = "COMPLETED"
)

type Event struct {
	ID              string    `json:"event_id"`
	RequestID       string    `json:"request_id"`
	ActorUserID     string    `json:"-"`
	Type            EventType `json:"event_type"`
	ReviewSessionID string    `json:"review_session_id,omitempty"`
	IdempotencyKey  string    `json:"-"`
	OccurredAt      time.Time `json:"occurred_at"`
}

type Summary struct {
	RequestID                 string   `json:"request_id"`
	CaptureStatus             string   `json:"capture_status"`
	RequestToResultDurationMS *int64   `json:"request_to_result_duration_ms"`
	ManualHandoffCount        *int64   `json:"manual_handoff_count"`
	ApprovalFollowupCount     *int64   `json:"approval_followup_count"`
	OperationReviewDurationMS *int64   `json:"operation_review_duration_ms"`
	ReviewStatus              string   `json:"review_status"`
	ReviewSessionID           *string  `json:"review_session_id"`
	ImprovementTargetPercent  *float64 `json:"improvement_target_percent"`
	CanRecordManualHandoff    bool     `json:"can_record_manual_handoff"`
	CanRecordApprovalFollowup bool     `json:"can_record_approval_followup"`
	CanStartReview            bool     `json:"can_start_review"`
	CanCompleteReview         bool     `json:"can_complete_review"`
}

type Repository interface {
	Record(context.Context, identity.User, Event) (Event, bool, error)
	Summarize(context.Context, identity.User, string) (Summary, error)
}

type Service struct {
	repository Repository
	newID      func() (string, error)
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, newID: randomUUID}
}

func (service *Service) Record(
	ctx context.Context,
	actor identity.User,
	requestID string,
	eventType EventType,
	reviewSessionID string,
	idempotencyKey string,
) (Event, bool, error) {
	if service == nil || service.repository == nil || !actor.OwnerActive || !validUUID(actor.ID) ||
		!validUUID(requestID) || !validUUID(idempotencyKey) {
		return Event{}, false, ErrInvalidInput
	}
	switch eventType {
	case EventManualHandoff, EventApprovalFollowup:
		if reviewSessionID != "" {
			return Event{}, false, ErrInvalidInput
		}
	case EventReviewStarted:
		if reviewSessionID != "" {
			return Event{}, false, ErrInvalidInput
		}
	case EventReviewCompleted:
		if !validUUID(reviewSessionID) {
			return Event{}, false, ErrInvalidInput
		}
	default:
		return Event{}, false, ErrInvalidInput
	}

	eventID, err := service.newID()
	if err != nil {
		return Event{}, false, fmt.Errorf("generate observation event ID: %w", err)
	}
	event := Event{
		ID: eventID, RequestID: requestID, ActorUserID: actor.ID,
		Type: eventType, ReviewSessionID: reviewSessionID, IdempotencyKey: idempotencyKey,
	}
	if eventType == EventReviewStarted {
		event.ReviewSessionID = event.ID
	}
	return service.repository.Record(ctx, actor, event)
}

func (service *Service) Summarize(ctx context.Context, actor identity.User, requestID string) (Summary, error) {
	if service == nil || service.repository == nil || !actor.OwnerActive || !validUUID(actor.ID) || !validUUID(requestID) {
		return Summary{}, ErrInvalidInput
	}
	return service.repository.Summarize(ctx, actor, requestID)
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded, err := hex.DecodeString(compact)
	return err == nil && len(decoded) == 16
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
