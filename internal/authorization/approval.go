package authorization

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/fallingnight/akv/internal/domain"
	"github.com/fallingnight/akv/internal/identity"
)

const MaximumGrantTTL = 10 * time.Minute

var (
	ErrDecisionConflict  = errors.New("authorization request already decided")
	ErrApprovalExpired   = errors.New("authorization approval expired")
	ErrApprovalForbidden = errors.New("approval forbidden")
)

type Decision string

const (
	DecisionApproved Decision = "APPROVED"
	DecisionRejected Decision = "REJECTED"
)

type Approval struct {
	ID             string
	RequestID      string
	ApproverUserID string
	Decision       Decision
	DecidedAt      time.Time
	GrantExpiresAt *time.Time
}

type Grant struct {
	ID            string
	RequestID     string
	AgentID       string
	TaskID        string
	TargetID      string
	CredentialID  string
	OperationHash [sha256.Size]byte
	ApprovedAt    time.Time
	ExpiresAt     time.Time
	Status        domain.GrantStatus
	ClaimedAt     *time.Time
	CompletedAt   *time.Time
	RevokedAt     *time.Time
}

type DecisionContext struct {
	Request          Request
	AgentOwnerUserID string
}

type DecisionCommand struct {
	Context  DecisionContext
	Approval Approval
	Grant    *Grant
	Now      time.Time
}

// DecisionRepository must implement DecidePending as one transaction whose
// conditional update wins only for PENDING_APPROVAL before the deadline.
type DecisionRepository interface {
	FindDecisionContext(context.Context, string) (DecisionContext, error)
	DecidePending(context.Context, DecisionCommand) error
	ExpirePending(context.Context, string, time.Time) error
}

type ApprovalService struct {
	repository DecisionRepository
	now        func() time.Time
	newID      func() (string, error)
}

func NewApprovalService(repository DecisionRepository) *ApprovalService {
	var sequence atomic.Uint64
	return &ApprovalService{
		repository: repository,
		now:        time.Now,
		newID: func() (string, error) {
			return fmt.Sprintf("generated-%d", sequence.Add(1)), nil
		},
	}
}

func (service *ApprovalService) Decide(
	ctx context.Context,
	actor identity.User,
	requestID string,
	decision Decision,
	requestedTTL *time.Duration,
) (Approval, *Grant, error) {
	if requestID == "" || decision != DecisionApproved && decision != DecisionRejected {
		return Approval{}, nil, ErrInvalidRequest
	}
	decisionContext, err := service.repository.FindDecisionContext(ctx, requestID)
	if err != nil {
		return Approval{}, nil, ErrDecisionConflict
	}
	if !actor.CanApprove(decisionContext.AgentOwnerUserID) {
		return Approval{}, nil, ErrApprovalForbidden
	}
	now := service.now()
	if !now.Before(decisionContext.Request.ApprovalDeadline) {
		_ = service.repository.ExpirePending(ctx, requestID, now)
		return Approval{}, nil, ErrApprovalExpired
	}
	approvalID, err := service.newID()
	if err != nil {
		return Approval{}, nil, fmt.Errorf("generate approval ID: %w", err)
	}
	approval := Approval{
		ID: approvalID, RequestID: requestID, ApproverUserID: actor.ID,
		Decision: decision, DecidedAt: now,
	}
	var grant *Grant
	if decision == DecisionApproved {
		ttl := MaximumGrantTTL
		if requestedTTL != nil {
			ttl = *requestedTTL
		}
		if ttl <= 0 || ttl > MaximumGrantTTL {
			return Approval{}, nil, ErrInvalidRequest
		}
		grantID, err := service.newID()
		if err != nil {
			return Approval{}, nil, fmt.Errorf("generate grant ID: %w", err)
		}
		expiresAt := now.Add(ttl)
		approval.GrantExpiresAt = &expiresAt
		request := decisionContext.Request
		grant = &Grant{
			ID: grantID, RequestID: request.ID, AgentID: request.AgentID, TaskID: request.TaskID,
			TargetID: request.TargetID, CredentialID: request.CredentialID,
			OperationHash: request.OperationHash, ApprovedAt: now, ExpiresAt: expiresAt,
			Status: domain.GrantApproved,
		}
	}
	if err := service.repository.DecidePending(ctx, DecisionCommand{
		Context: decisionContext, Approval: approval, Grant: grant, Now: now,
	}); err != nil {
		if errors.Is(err, ErrApprovalExpired) {
			return Approval{}, nil, ErrApprovalExpired
		}
		return Approval{}, nil, ErrDecisionConflict
	}
	return approval, grant, nil
}
