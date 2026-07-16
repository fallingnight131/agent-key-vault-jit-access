package observation

import (
	"context"
	"errors"
	"testing"

	"github.com/fallingnight/akv/internal/identity"
)

const (
	testActorID       = "00000000-0000-4000-8000-000000000001"
	testRequestID     = "00000000-0000-4000-8000-000000000002"
	testEventID       = "00000000-0000-4000-8000-000000000003"
	testReviewID      = "00000000-0000-4000-8000-000000000004"
	testIdempotencyID = "00000000-0000-4000-8000-000000000005"
)

type observationRepositoryFake struct {
	event   Event
	summary Summary
}

func (fake *observationRepositoryFake) Record(_ context.Context, _ identity.User, event Event) (Event, bool, error) {
	fake.event = event
	return event, true, nil
}

func (fake *observationRepositoryFake) Summarize(_ context.Context, _ identity.User, _ string) (Summary, error) {
	return fake.summary, nil
}

func TestRecordValidatesShapeAndGeneratesReviewSession(t *testing.T) {
	repository := &observationRepositoryFake{}
	service := NewService(repository)
	service.newID = func() (string, error) { return testEventID, nil }
	actor := identity.User{ID: testActorID, OwnerActive: true}

	event, created, err := service.Record(
		context.Background(), actor, testRequestID, EventReviewStarted, "", testIdempotencyID,
	)
	if err != nil || !created || event.ID != testEventID || event.ReviewSessionID != testEventID ||
		repository.event.Type != EventReviewStarted || repository.event.IdempotencyKey != testIdempotencyID {
		t.Fatalf("Record() event=%+v created=%t error=%v", event, created, err)
	}

	for name, fixture := range map[string]struct {
		eventType EventType
		reviewID  string
		key       string
	}{
		"unknown event":        {eventType: "UNKNOWN", key: testIdempotencyID},
		"handoff review id":    {eventType: EventManualHandoff, reviewID: testReviewID, key: testIdempotencyID},
		"completion no review": {eventType: EventReviewCompleted, key: testIdempotencyID},
		"invalid idempotency":  {eventType: EventManualHandoff, key: "not-a-uuid"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := service.Record(
				context.Background(), actor, testRequestID, fixture.eventType, fixture.reviewID, fixture.key,
			)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Record() error=%v", err)
			}
		})
	}
}

func TestSummarizeRejectsInactiveOrMalformedContext(t *testing.T) {
	service := NewService(&observationRepositoryFake{})
	for _, fixture := range []struct {
		actor     identity.User
		requestID string
	}{
		{actor: identity.User{ID: testActorID}, requestID: testRequestID},
		{actor: identity.User{ID: "invalid", OwnerActive: true}, requestID: testRequestID},
		{actor: identity.User{ID: testActorID, OwnerActive: true}, requestID: "invalid"},
	} {
		if _, err := service.Summarize(context.Background(), fixture.actor, fixture.requestID); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("Summarize() error=%v", err)
		}
	}
}
