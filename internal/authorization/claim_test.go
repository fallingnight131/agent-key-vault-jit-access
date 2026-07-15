package authorization

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/domain"
)

type fakeClaimRepository struct {
	mutex      sync.Mutex
	grant      Grant
	taskActive bool
	claims     int
}

func (repository *fakeClaimRepository) ClaimApproved(_ context.Context, claim ClaimContext, now time.Time) (Grant, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.claims++
	grant := &repository.grant
	if grant.Status != domain.GrantApproved || !repository.taskActive || !now.Before(grant.ExpiresAt) ||
		grant.ID != claim.GrantID || grant.AgentID != claim.AgentID || grant.TaskID != claim.TaskID ||
		grant.TargetID != claim.TargetID || grant.CredentialID != claim.CredentialID || grant.OperationHash != claim.OperationHash {
		if grant.Status == domain.GrantApproved && !now.Before(grant.ExpiresAt) {
			grant.Status = domain.GrantExpired
		}
		return Grant{}, ErrClaimDenied
	}
	grant.Status = domain.GrantExecuting
	grant.ClaimedAt = &now
	return *grant, nil
}

func TestClaimRejectsReplayAndConcurrentUse(t *testing.T) {
	repository, guard, claim := newClaimGuard()
	const contenders = 32
	start := make(chan struct{})
	results := make(chan error, contenders)
	var wait sync.WaitGroup
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := guard.Claim(context.Background(), claim)
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	winners, denied := 0, 0
	for err := range results {
		if err == nil {
			winners++
		} else if errors.Is(err, ErrClaimDenied) {
			denied++
		}
	}
	if winners != 1 || denied != contenders-1 || repository.grant.Status != domain.GrantExecuting {
		t.Fatalf("winners=%d denied=%d status=%s", winners, denied, repository.grant.Status)
	}
	if _, err := guard.Claim(context.Background(), claim); !errors.Is(err, ErrClaimDenied) {
		t.Fatalf("replay Claim() error = %v", err)
	}
}

func TestClaimRejectsEveryContextMismatch(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*ClaimContext)
	}{
		{"grant", func(claim *ClaimContext) { claim.GrantID = "other" }},
		{"agent", func(claim *ClaimContext) { claim.AgentID = "other" }},
		{"task", func(claim *ClaimContext) { claim.TaskID = "other" }},
		{"target", func(claim *ClaimContext) { claim.TargetID = "other" }},
		{"credential", func(claim *ClaimContext) { claim.CredentialID = "other" }},
		{"operation", func(claim *ClaimContext) { claim.OperationHash[0]++ }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			repository, guard, claim := newClaimGuard()
			test.mutate(&claim)
			if _, err := guard.Claim(context.Background(), claim); !errors.Is(err, ErrClaimDenied) {
				t.Fatalf("Claim() error = %v", err)
			}
			if repository.grant.Status != domain.GrantApproved {
				t.Fatalf("status = %s", repository.grant.Status)
			}
		})
	}
}

func TestClaimRejectsExpiredRevokedAndInactiveTask(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeClaimRepository, *ExecutionGuard)
	}{
		{"expired", func(repository *fakeClaimRepository, guard *ExecutionGuard) {
			guard.now = func() time.Time { return repository.grant.ExpiresAt }
		}},
		{"revoked", func(repository *fakeClaimRepository, _ *ExecutionGuard) {
			repository.grant.Status = domain.GrantRevoked
		}},
		{"inactive task", func(repository *fakeClaimRepository, _ *ExecutionGuard) { repository.taskActive = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository, guard, claim := newClaimGuard()
			test.mutate(repository, guard)
			if _, err := guard.Claim(context.Background(), claim); !errors.Is(err, ErrClaimDenied) {
				t.Fatalf("Claim() error = %v", err)
			}
			if repository.grant.ClaimedAt != nil {
				t.Fatal("denied grant was claimed")
			}
		})
	}
}

func newClaimGuard() (*fakeClaimRepository, *ExecutionGuard, ClaimContext) {
	now := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
	grant := Grant{
		ID: "grant", RequestID: "request", AgentID: "agent", TaskID: "task",
		TargetID: "target", CredentialID: "credential", Status: domain.GrantApproved,
		ApprovedAt: now, ExpiresAt: now.Add(MaximumGrantTTL),
	}
	grant.OperationHash[0] = 42
	repository := &fakeClaimRepository{grant: grant, taskActive: true}
	guard := NewExecutionGuard(repository)
	guard.now = func() time.Time { return now.Add(time.Minute) }
	claim := ClaimContext{
		GrantID: grant.ID, AgentID: grant.AgentID, TaskID: grant.TaskID,
		TargetID: grant.TargetID, CredentialID: grant.CredentialID, OperationHash: grant.OperationHash,
	}
	return repository, guard, claim
}
