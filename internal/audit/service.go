package audit

import (
	"context"
	"errors"
	"regexp"
	"time"
)

const (
	RetentionPeriod = 180 * 24 * time.Hour
	CleanupBatch    = 1000
)

var (
	ErrUnsafeEvent = errors.New("unsafe audit event")
	safeValue      = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
)

type Event struct {
	ID          string
	Type        string
	ActorType   string
	ActorID     *string
	RequestID   *string
	ApprovalID  *string
	GrantID     *string
	ExecutionID *string
	ReclaimID   *string
	Metadata    map[string]string
	CreatedAt   time.Time
}

type Repository interface {
	Append(context.Context, Event) error
	DeleteBefore(context.Context, time.Time, int) (int64, error)
}

type Service struct {
	repository Repository
	now        func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, now: time.Now}
}

func (service *Service) Record(ctx context.Context, event Event) error {
	if !safeValue.MatchString(event.Type) || !safeValue.MatchString(event.ActorType) {
		return ErrUnsafeEvent
	}
	allowedKeys := map[string]bool{
		"status": true, "error_code": true, "connector_type": true,
		"operation_kind": true, "decision": true, "token_lifetime": true,
	}
	for key, value := range event.Metadata {
		if !allowedKeys[key] || !safeValue.MatchString(value) {
			return ErrUnsafeEvent
		}
	}
	event.CreatedAt = service.now()
	if err := service.repository.Append(ctx, event); err != nil {
		return err
	}
	return nil
}

func (service *Service) Cleanup(ctx context.Context) (int64, error) {
	return service.repository.DeleteBefore(ctx, service.now().Add(-RetentionPeriod), CleanupBatch)
}
