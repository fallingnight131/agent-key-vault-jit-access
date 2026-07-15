package proxy

import (
	"context"
	"time"

	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/domain"
	"github.com/fallingnight/akv/internal/vault"
)

type SignProxy struct {
	plans     PlanStore
	guard     Guard
	vault     vault.ExecutionClient
	lifecycle Lifecycle
	now       func() time.Time
}

func NewSignProxy(plans PlanStore, guard Guard, vaultClient vault.ExecutionClient, lifecycle Lifecycle) *SignProxy {
	return &SignProxy{plans: plans, guard: guard, vault: vaultClient, lifecycle: lifecycle, now: time.Now}
}

func (proxy *SignProxy) Execute(ctx context.Context, requestID, authenticatedAgentID, taskID string) ([]byte, error) {
	plan, err := proxy.plans.LoadPlan(ctx, requestID)
	if err != nil || plan.AgentID != authenticatedAgentID || plan.TaskID != taskID ||
		plan.Credential.Type != catalog.CredentialTransitKey || !plan.Credential.Active ||
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
	signature, err := proxy.vault.Sign(ctx, plan.Credential.VaultPath, plan.Operation.Sign.DigestAlgorithm, plan.Operation.Sign.Digest)
	if err != nil {
		_ = proxy.lifecycle.Finish(ctx, executionID, domain.ExecutionFailed, proxy.now(), "SIGN_FAILED")
		return nil, &PublicError{Code: "SIGN_FAILED"}
	}
	_ = proxy.lifecycle.Finish(ctx, executionID, domain.ExecutionSucceeded, proxy.now(), "")
	return signature, nil
}
