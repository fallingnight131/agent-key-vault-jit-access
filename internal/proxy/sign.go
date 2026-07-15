package proxy

import (
	"context"
	"errors"
	"time"

	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/domain"
	"github.com/fallingnight/akv/internal/vault"
)

type SignProxy struct {
	plans         PlanStore
	guard         Guard
	vault         vault.ExecutionClient
	lifecycle     Lifecycle
	now           func() time.Time
	cancellations *CancellationRegistry
}

func NewSignProxy(plans PlanStore, guard Guard, vaultClient vault.ExecutionClient, lifecycle Lifecycle) *SignProxy {
	return &SignProxy{plans: plans, guard: guard, vault: vaultClient, lifecycle: lifecycle, now: time.Now, cancellations: NewCancellationRegistry()}
}

func (proxy *SignProxy) SetCancellationRegistry(registry *CancellationRegistry) {
	proxy.cancellations = registry
}

func (proxy *SignProxy) Execute(ctx context.Context, requestID, authenticatedAgentID, taskID string) ([]byte, error) {
	plan, err := proxy.plans.LoadPlan(ctx, requestID)
	if err != nil || plan.AgentID != authenticatedAgentID || plan.TaskID != taskID ||
		!plan.Target.Active || !plan.Credential.Active || plan.Credential.Type != catalog.CredentialTransitKey ||
		plan.Operation.Kind != authorization.OperationSign || plan.Operation.Sign == nil {
		return nil, ErrExecutionDenied
	}
	grant, err := proxy.guard.Claim(ctx, authorization.ClaimContext{
		GrantID: plan.GrantID, AgentID: authenticatedAgentID, TaskID: taskID,
		TargetID: plan.Target.ID, CredentialID: plan.Credential.ID, OperationHash: plan.OperationHash,
	})
	if err != nil {
		return nil, ErrExecutionDenied
	}
	executionID, err := proxy.lifecycle.Start(ctx, grant, proxy.now())
	if err != nil {
		return nil, &PublicError{Code: "EXECUTION_STATE_FAILED"}
	}
	executionContext, release := proxy.cancellations.Track(ctx, executionID)
	defer release()
	signature, err := proxy.vault.Sign(executionContext, plan.Credential.VaultPath, plan.Operation.Sign.DigestAlgorithm, plan.Operation.Sign.Digest)
	if err != nil {
		status, code := domain.ExecutionFailed, "SIGN_FAILED"
		if errors.Is(executionContext.Err(), context.Canceled) {
			status, code = domain.ExecutionCancelled, "SIGN_CANCELLED"
		}
		if finalError := finalizeExecution(ctx, proxy.lifecycle, executionID, status, code, nil, proxy.now); finalError != nil {
			return nil, finalError
		}
		return nil, &PublicError{Code: "SIGN_FAILED"}
	}
	if err := finalizeExecution(ctx, proxy.lifecycle, executionID, domain.ExecutionSucceeded, "", nil, proxy.now); err != nil {
		return nil, err
	}
	return signature, nil
}
