package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/agent"
	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/identity"
	"github.com/fallingnight/akv/internal/task"
)

type fakeRepository struct {
	now, cutoff time.Time
	revokes     int
}

func (*fakeRepository) FindDecisionContext(context.Context, string) (authorization.DecisionContext, error) {
	return authorization.DecisionContext{Request: authorization.Request{AgentID: "agent-a"}, AgentOwnerUserID: "owner"}, nil
}

func TestAgentRevokeRequiresExactAgentBinding(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)
	if _, err := service.RevokeAgent(context.Background(), agent.Principal{AgentID: "agent-b", OwnerUserID: "owner"}, "request"); !errors.Is(err, ErrRevokeForbidden) {
		t.Fatalf("cross-agent RevokeAgent() error=%v", err)
	}
	if _, err := service.RevokeAgent(context.Background(), agent.Principal{AgentID: "agent-a", OwnerUserID: "owner"}, "request"); err != nil {
		t.Fatalf("bound RevokeAgent() error=%v", err)
	}
	if repository.revokes != 1 {
		t.Fatalf("revokes=%d", repository.revokes)
	}
}
func (repository *fakeRepository) RevokeRequest(context.Context, string, time.Time) (RevokeResult, error) {
	repository.revokes++
	return RevokeResult{RevokedBeforeExecution: true}, nil
}
func (repository *fakeRepository) SweepExpiredAndLost(_ context.Context, now, cutoff time.Time) (SweepResult, error) {
	repository.now, repository.cutoff = now, cutoff
	return SweepResult{ExpiredRequests: 1}, nil
}

func TestRevokePermissions(t *testing.T) {
	for _, test := range []struct {
		name    string
		actor   identity.User
		allowed bool
	}{
		{"owner", identity.User{ID: "owner", OwnerActive: true}, true},
		{"other", identity.User{ID: "other", OwnerActive: true}, false},
		{"approve all", identity.User{ID: "other", ApproveAll: true, OwnerActive: true}, true},
		{"admin", identity.User{ID: "admin", IsAdmin: true, OwnerActive: true}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{}
			_, err := NewService(repository).Revoke(context.Background(), test.actor, "request")
			if test.allowed && err != nil {
				t.Fatalf("Revoke() error = %v", err)
			}
			if !test.allowed && !errors.Is(err, ErrRevokeForbidden) {
				t.Fatalf("Revoke() error = %v", err)
			}
		})
	}
}

func TestSweepUsesAgentLostBoundary(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)
	now := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
	service.now = func() time.Time { return now }
	if _, err := service.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if repository.now != now || repository.cutoff != now.Add(-task.AgentLostAfter) {
		t.Fatalf("now=%v cutoff=%v", repository.now, repository.cutoff)
	}
}
