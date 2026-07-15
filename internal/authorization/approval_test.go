package authorization

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/domain"
	"github.com/fallingnight/akv/internal/identity"
)

type fakeDecisionRepository struct {
	mutex    sync.Mutex
	context  DecisionContext
	approval *Approval
	grant    *Grant
}

func (repository *fakeDecisionRepository) FindDecisionContext(_ context.Context, requestID string) (DecisionContext, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.context.Request.ID != requestID {
		return DecisionContext{}, ErrDecisionConflict
	}
	return repository.context, nil
}

func (repository *fakeDecisionRepository) DecidePending(_ context.Context, command DecisionCommand) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.context.Request.Status != domain.RequestPendingApproval || repository.approval != nil {
		return ErrDecisionConflict
	}
	if !command.Now.Before(repository.context.Request.ApprovalDeadline) {
		repository.context.Request.Status = domain.RequestApprovalExpired
		return ErrApprovalExpired
	}
	if command.Context.Request.OperationHash != repository.context.Request.OperationHash {
		return ErrDecisionConflict
	}
	if command.Approval.Decision == DecisionApproved && command.Grant == nil || command.Approval.Decision == DecisionRejected && command.Grant != nil {
		return ErrDecisionConflict
	}
	repository.approval = &command.Approval
	repository.grant = command.Grant
	if command.Approval.Decision == DecisionApproved {
		repository.context.Request.Status = domain.RequestApproved
	} else {
		repository.context.Request.Status = domain.RequestRejected
	}
	return nil
}

func (repository *fakeDecisionRepository) ExpirePending(_ context.Context, requestID string, _ time.Time) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.context.Request.ID != requestID || repository.context.Request.Status != domain.RequestPendingApproval {
		return ErrDecisionConflict
	}
	repository.context.Request.Status = domain.RequestApprovalExpired
	return nil
}

func TestApprovalPermissions(t *testing.T) {
	actors := []struct {
		name    string
		actor   identity.User
		allowed bool
	}{
		{"owner", identity.User{ID: "owner", OwnerActive: true}, true},
		{"other", identity.User{ID: "other", OwnerActive: true}, false},
		{"approve all", identity.User{ID: "other", ApproveAll: true, OwnerActive: true}, true},
		{"admin", identity.User{ID: "admin", IsAdmin: true, OwnerActive: true}, true},
		{"disabled admin", identity.User{ID: "admin", IsAdmin: true}, false},
	}
	for _, test := range actors {
		t.Run(test.name, func(t *testing.T) {
			repository, service, _ := newApprovalService()
			_, _, err := service.Decide(context.Background(), test.actor, repository.context.Request.ID, DecisionRejected, nil)
			if test.allowed && err != nil {
				t.Fatalf("Decide() error = %v", err)
			}
			if !test.allowed && !errors.Is(err, ErrApprovalForbidden) {
				t.Fatalf("Decide() error = %v", err)
			}
		})
	}
}

func TestApproveCreatesBoundGrantWithMaximumOrShorterTTL(t *testing.T) {
	for _, test := range []struct {
		name string
		ttl  *time.Duration
		want time.Duration
	}{
		{"default", nil, MaximumGrantTTL},
		{"shorter", durationPointer(time.Minute), time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository, service, now := newApprovalService()
			approval, grant, err := service.Decide(context.Background(), identity.User{ID: "owner", OwnerActive: true}, repository.context.Request.ID, DecisionApproved, test.ttl)
			if err != nil {
				t.Fatalf("Decide() error = %v", err)
			}
			request := repository.context.Request
			if grant.AgentID != request.AgentID || grant.TaskID != request.TaskID || grant.TargetID != request.TargetID || grant.CredentialID != request.CredentialID || grant.OperationHash != request.OperationHash {
				t.Fatalf("grant not bound to request: %+v", grant)
			}
			if grant.ExpiresAt.Sub(now) != test.want || *approval.GrantExpiresAt != grant.ExpiresAt {
				t.Fatalf("grant TTL = %v", grant.ExpiresAt.Sub(now))
			}
		})
	}
}

func TestRejectAndExpireCreateNoGrant(t *testing.T) {
	repository, service, now := newApprovalService()
	if _, grant, err := service.Decide(context.Background(), identity.User{ID: "owner", OwnerActive: true}, repository.context.Request.ID, DecisionRejected, nil); err != nil || grant != nil || repository.grant != nil {
		t.Fatalf("reject result grant=%+v stored=%+v err=%v", grant, repository.grant, err)
	}

	repository, service, _ = newApprovalService()
	service.now = func() time.Time { return now.Add(ApprovalWait) }
	if _, grant, err := service.Decide(context.Background(), identity.User{ID: "owner", OwnerActive: true}, repository.context.Request.ID, DecisionApproved, nil); !errors.Is(err, ErrApprovalExpired) || grant != nil || repository.grant != nil {
		t.Fatalf("expired result grant=%+v stored=%+v err=%v", grant, repository.grant, err)
	}
}

func TestConcurrentDecisionHasSingleWinner(t *testing.T) {
	repository, service, _ := newApprovalService()
	var sequence atomic.Uint64
	service.newID = func() (string, error) { return fmt.Sprintf("id-%d", sequence.Add(1)), nil }
	actor := identity.User{ID: "owner", OwnerActive: true}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, decision := range []Decision{DecisionApproved, DecisionRejected} {
		wait.Add(1)
		go func(decision Decision) {
			defer wait.Done()
			<-start
			_, _, err := service.Decide(context.Background(), actor, repository.context.Request.ID, decision, nil)
			results <- err
		}(decision)
	}
	close(start)
	wait.Wait()
	close(results)
	winners, conflicts := 0, 0
	for err := range results {
		if err == nil {
			winners++
		} else if errors.Is(err, ErrDecisionConflict) {
			conflicts++
		}
	}
	if winners != 1 || conflicts != 1 || repository.approval == nil {
		t.Fatalf("winners=%d conflicts=%d approval=%+v", winners, conflicts, repository.approval)
	}
	if repository.approval.Decision == DecisionRejected && repository.grant != nil {
		t.Fatal("rejected winning decision created grant")
	}
}

func newApprovalService() (*fakeDecisionRepository, *ApprovalService, time.Time) {
	now := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
	repository := &fakeDecisionRepository{context: DecisionContext{
		Request: Request{
			ID: "request", AgentID: "agent", TaskID: "task", TargetID: "target", CredentialID: "credential",
			Status: domain.RequestPendingApproval, CreatedAt: now, ApprovalDeadline: now.Add(ApprovalWait),
		},
		AgentOwnerUserID: "owner",
	}}
	service := NewApprovalService(repository)
	service.now = func() time.Time { return now }
	var sequence atomic.Uint64
	service.newID = func() (string, error) { return fmt.Sprintf("id-%d", sequence.Add(1)), nil }
	return repository, service, now
}

func durationPointer(value time.Duration) *time.Duration { return &value }
