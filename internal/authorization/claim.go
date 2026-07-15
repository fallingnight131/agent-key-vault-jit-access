package authorization

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"
)

var ErrClaimDenied = errors.New("operation grant claim denied")

type ClaimContext struct {
	GrantID       string
	AgentID       string
	TaskID        string
	TargetID      string
	CredentialID  string
	OperationHash [sha256.Size]byte
}

// ClaimRepository must perform one conditional persistent transition from
// APPROVED to EXECUTING while also checking the active task and full context.
type ClaimRepository interface {
	ClaimApproved(context.Context, ClaimContext, time.Time) (Grant, error)
}

type ExecutionGuard struct {
	repository ClaimRepository
	now        func() time.Time
}

func NewExecutionGuard(repository ClaimRepository) *ExecutionGuard {
	return &ExecutionGuard{repository: repository, now: time.Now}
}

// Claim is deliberately the guard's only operation: it neither reads a Grant
// first nor has any Vault or connector capability.
func (guard *ExecutionGuard) Claim(ctx context.Context, claim ClaimContext) (Grant, error) {
	if claim.GrantID == "" || claim.AgentID == "" || claim.TaskID == "" || claim.TargetID == "" || claim.CredentialID == "" {
		return Grant{}, ErrClaimDenied
	}
	grant, err := guard.repository.ClaimApproved(ctx, claim, guard.now())
	if err != nil {
		return Grant{}, ErrClaimDenied
	}
	return grant, nil
}
