package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fallingnight/akv/internal/agent"
	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/identity"
	"github.com/fallingnight/akv/internal/task"
)

var (
	ErrRevokeForbidden   = errors.New("revoke forbidden")
	ErrRevokeUnavailable = errors.New("authorization cannot be revoked")
)

type RevokeActor struct {
	Type string
	ID   string
}

type SweepResult struct {
	ExpiredRequests     int64
	ExpiredGrants       int64
	LostTasks           int64
	CancelledExecutions []string
}

func (service *Service) RevokeAgent(ctx context.Context, principal agent.Principal, requestID string) (RevokeResult, error) {
	decisionContext, err := service.repository.FindDecisionContext(ctx, requestID)
	if err != nil || principal.AgentID == "" || decisionContext.Request.AgentID != principal.AgentID {
		return RevokeResult{}, ErrRevokeForbidden
	}
	result, err := service.repository.RevokeRequest(ctx, requestID, RevokeActor{Type: "AGENT", ID: principal.AgentID}, service.now())
	if err != nil {
		return RevokeResult{}, ErrRevokeUnavailable
	}
	return result, nil
}

type RevokeResult struct {
	RevokedBeforeExecution bool
	CancelExecutionIDs     []string
}

type Repository interface {
	FindDecisionContext(context.Context, string) (authorization.DecisionContext, error)
	RevokeRequest(context.Context, string, RevokeActor, time.Time) (RevokeResult, error)
	SweepExpiredAndLost(context.Context, time.Time, time.Time) (SweepResult, error)
}

type Service struct {
	repository Repository
	now        func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, now: time.Now}
}

func (service *Service) Revoke(ctx context.Context, actor identity.User, requestID string) (RevokeResult, error) {
	decisionContext, err := service.repository.FindDecisionContext(ctx, requestID)
	if err != nil {
		return RevokeResult{}, ErrRevokeUnavailable
	}
	if !actor.CanApprove(decisionContext.AgentOwnerUserID) {
		return RevokeResult{}, ErrRevokeForbidden
	}
	result, err := service.repository.RevokeRequest(ctx, requestID, RevokeActor{Type: "USER", ID: actor.ID}, service.now())
	if err != nil {
		return RevokeResult{}, ErrRevokeUnavailable
	}
	return result, nil
}

func (service *Service) Sweep(ctx context.Context) (SweepResult, error) {
	now := service.now()
	result, err := service.repository.SweepExpiredAndLost(ctx, now, now.Add(-task.AgentLostAfter))
	if err != nil {
		return SweepResult{}, fmt.Errorf("sweep lifecycle: %w", err)
	}
	return result, nil
}
