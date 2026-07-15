package proxy

import (
	"context"
	"time"

	"github.com/fallingnight/akv/internal/domain"
)

const ReclaimDeadline = 5 * time.Second

type Cleanup func(context.Context) error

func finalizeExecution(
	requestContext context.Context,
	lifecycle Lifecycle,
	executionID string,
	outcome domain.ExecutionStatus,
	outcomeCode string,
	cleanup Cleanup,
	now func() time.Time,
) error {
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(requestContext), ReclaimDeadline)
	defer cancel()
	if err := lifecycle.Finish(cleanupContext, executionID, outcome, now(), outcomeCode); err != nil {
		return &PublicError{Code: "EXECUTION_STATE_FAILED"}
	}
	reclaimID, err := lifecycle.StartReclaim(cleanupContext, executionID, now())
	if err != nil {
		return &PublicError{Code: "RECLAIM_FAILED"}
	}
	cleanupError := error(nil)
	if cleanup != nil {
		cleanupError = cleanup(cleanupContext)
	}
	success, errorCode := cleanupError == nil, ""
	if !success {
		errorCode = "RESOURCE_CLEANUP_FAILED"
	}
	if err := lifecycle.FinishReclaim(cleanupContext, reclaimID, success, now(), errorCode); err != nil || !success {
		return &PublicError{Code: "RECLAIM_FAILED"}
	}
	return nil
}
