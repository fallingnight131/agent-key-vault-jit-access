package audit

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeRepository struct {
	event  Event
	before time.Time
	limit  int
}

func (repository *fakeRepository) Append(_ context.Context, event Event) error {
	repository.event = event
	return nil
}
func (repository *fakeRepository) DeleteBefore(_ context.Context, before time.Time, limit int) (int64, error) {
	repository.before, repository.limit = before, limit
	return 2, nil
}

func TestAuditRejectsSensitiveOrArbitraryMetadata(t *testing.T) {
	service := NewService(&fakeRepository{})
	for _, metadata := range []map[string]string{
		{"password": "fixture"},
		{"error_code": "backend returned secret with spaces"},
		{"authorization": "Bearer.fixture"},
	} {
		if err := service.Record(context.Background(), Event{Type: "grant.claimed", ActorType: "SYSTEM", Metadata: metadata}); !errors.Is(err, ErrUnsafeEvent) {
			t.Fatalf("Record(%v) error = %v", metadata, err)
		}
	}
}

func TestAuditCleanupUses180DaysAndBoundedBatch(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)
	now := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
	service.now = func() time.Time { return now }
	deleted, err := service.Cleanup(context.Background())
	if err != nil || deleted != 2 || repository.before != now.Add(-RetentionPeriod) || repository.limit != CleanupBatch {
		t.Fatalf("deleted=%d before=%v limit=%d error=%v", deleted, repository.before, repository.limit, err)
	}
}
