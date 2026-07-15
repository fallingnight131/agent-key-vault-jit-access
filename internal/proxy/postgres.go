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

const (
	PostgreSQLStatementTimeout = 60 * time.Second
	PostgreSQLBatchTimeout     = 5 * time.Minute
)

type SQLResult interface {
	RowsAffected() (int64, error)
}

type SQLDatabase interface {
	ExecContext(context.Context, string, ...any) (SQLResult, error)
	BeginTx(context.Context) (SQLTransaction, error)
	Close() error
}

type SQLTransaction interface {
	ExecContext(context.Context, string, ...any) (SQLResult, error)
	Commit() error
	Rollback() error
}

type SQLConnectionFactory interface {
	Connect(context.Context, catalog.ConnectionConfig, map[string]*vault.SensitiveValue) (SQLDatabase, error)
}

type PostgreSQLResult struct {
	RowsAffected []int64
}

type PostgreSQLProxy struct {
	plans         PlanStore
	guard         Guard
	vault         vault.ExecutionClient
	lifecycle     Lifecycle
	factory       SQLConnectionFactory
	now           func() time.Time
	cancellations *CancellationRegistry
}

func NewPostgreSQLProxy(plans PlanStore, guard Guard, vaultClient vault.ExecutionClient, lifecycle Lifecycle, factory SQLConnectionFactory) *PostgreSQLProxy {
	return &PostgreSQLProxy{plans: plans, guard: guard, vault: vaultClient, lifecycle: lifecycle, factory: factory, now: time.Now, cancellations: NewCancellationRegistry()}
}

func (proxy *PostgreSQLProxy) SetCancellationRegistry(registry *CancellationRegistry) {
	proxy.cancellations = registry
}

func (proxy *PostgreSQLProxy) Execute(ctx context.Context, requestID, authenticatedAgentID, taskID string) (PostgreSQLResult, error) {
	plan, err := proxy.plans.LoadPlan(ctx, requestID)
	if err != nil || plan.AgentID != authenticatedAgentID || plan.TaskID != taskID ||
		(plan.Operation.Kind != authorization.OperationPostgreSQLStatement && plan.Operation.Kind != authorization.OperationPostgreSQLBatch) ||
		plan.Operation.PostgreSQL == nil || len(plan.Operation.PostgreSQL.Statements) == 0 ||
		!plan.Target.Active || !plan.Credential.Active || plan.Target.ConnectorType != catalog.ConnectorPostgreSQL {
		return PostgreSQLResult{}, ErrExecutionDenied
	}
	grant, err := proxy.guard.Claim(ctx, authorization.ClaimContext{
		GrantID: plan.GrantID, AgentID: authenticatedAgentID, TaskID: taskID,
		TargetID: plan.Target.ID, CredentialID: plan.Credential.ID, OperationHash: plan.OperationHash,
	})
	if err != nil {
		return PostgreSQLResult{}, ErrExecutionDenied
	}
	executionID, err := proxy.lifecycle.Start(ctx, grant, proxy.now())
	if err != nil {
		return PostgreSQLResult{}, &PublicError{Code: "EXECUTION_STATE_FAILED"}
	}
	executionContext, release := proxy.cancellations.Track(ctx, executionID)
	defer release()
	referenceKind := vault.ReferenceStaticKV
	credentialTTL := PostgreSQLStatementTimeout
	if plan.Operation.Kind == authorization.OperationPostgreSQLBatch {
		credentialTTL = PostgreSQLBatchTimeout
	}
	if plan.Credential.Type == catalog.CredentialPostgreSQLDynamic {
		referenceKind = vault.ReferencePostgreSQLDynamic
	} else if plan.Target.ConnectionConfig.RequireDynamic {
		if finalError := finalizeExecution(ctx, proxy.lifecycle, executionID, domain.ExecutionFailed, "DYNAMIC_CREDENTIAL_REQUIRED", nil, proxy.now); finalError != nil {
			return PostgreSQLResult{}, finalError
		}
		return PostgreSQLResult{}, &PublicError{Code: "DYNAMIC_CREDENTIAL_REQUIRED"}
	}
	handle, err := vault.Acquire(executionContext, proxy.vault, vault.Reference{
		Kind: referenceKind, Path: plan.Credential.VaultPath, Version: plan.Credential.VaultVersion,
		TTL: credentialTTL,
	})
	if err != nil {
		if finalError := finalizeExecution(ctx, proxy.lifecycle, executionID, domain.ExecutionFailed, "VAULT_UNAVAILABLE", nil, proxy.now); finalError != nil {
			return PostgreSQLResult{}, finalError
		}
		return PostgreSQLResult{}, &PublicError{Code: "VAULT_UNAVAILABLE"}
	}
	handleCleanup := func(cleanupContext context.Context) error { return handle.Close(cleanupContext) }
	database, err := proxy.factory.Connect(executionContext, plan.Target.ConnectionConfig, handle.Values)
	if err != nil {
		if finalError := finalizeExecution(ctx, proxy.lifecycle, executionID, domain.ExecutionFailed, "TARGET_UNAVAILABLE", handleCleanup, proxy.now); finalError != nil {
			return PostgreSQLResult{}, finalError
		}
		return PostgreSQLResult{}, &PublicError{Code: "TARGET_UNAVAILABLE"}
	}
	cleanup := func(cleanupContext context.Context) error {
		return errors.Join(database.Close(), handle.Close(cleanupContext))
	}

	if plan.Operation.Kind == authorization.OperationPostgreSQLStatement {
		return proxy.executeStatement(executionContext, executionID, database, plan.Operation.PostgreSQL.Statements[0], cleanup)
	}
	return proxy.executeBatch(executionContext, executionID, database, plan.Operation.PostgreSQL.Statements, cleanup)
}

func (proxy *PostgreSQLProxy) executeStatement(ctx context.Context, executionID string, database SQLDatabase, statement authorization.SQLStatement, cleanup Cleanup) (PostgreSQLResult, error) {
	timeoutContext, cancel := context.WithTimeout(ctx, PostgreSQLStatementTimeout)
	defer cancel()
	result, err := database.ExecContext(timeoutContext, statement.SQL, statement.Arguments...)
	if err != nil {
		status, code := databaseFailure(timeoutContext)
		if finalError := finalizeExecution(ctx, proxy.lifecycle, executionID, status, code, cleanup, proxy.now); finalError != nil {
			return PostgreSQLResult{}, finalError
		}
		return PostgreSQLResult{}, &PublicError{Code: code}
	}
	rows, err := result.RowsAffected()
	if err != nil {
		if finalError := finalizeExecution(ctx, proxy.lifecycle, executionID, domain.ExecutionFailed, "TARGET_RESULT_INVALID", cleanup, proxy.now); finalError != nil {
			return PostgreSQLResult{}, finalError
		}
		return PostgreSQLResult{}, &PublicError{Code: "TARGET_RESULT_INVALID"}
	}
	if err := finalizeExecution(ctx, proxy.lifecycle, executionID, domain.ExecutionSucceeded, "", cleanup, proxy.now); err != nil {
		return PostgreSQLResult{}, err
	}
	return PostgreSQLResult{RowsAffected: []int64{rows}}, nil
}

func (proxy *PostgreSQLProxy) executeBatch(ctx context.Context, executionID string, database SQLDatabase, statements []authorization.SQLStatement, cleanup Cleanup) (PostgreSQLResult, error) {
	timeoutContext, cancel := context.WithTimeout(ctx, PostgreSQLBatchTimeout)
	defer cancel()
	transaction, err := database.BeginTx(timeoutContext)
	if err != nil {
		if finalError := finalizeExecution(ctx, proxy.lifecycle, executionID, domain.ExecutionFailed, "TARGET_UNAVAILABLE", cleanup, proxy.now); finalError != nil {
			return PostgreSQLResult{}, finalError
		}
		return PostgreSQLResult{}, &PublicError{Code: "TARGET_UNAVAILABLE"}
	}
	rows := make([]int64, 0, len(statements))
	for _, statement := range statements {
		result, err := transaction.ExecContext(timeoutContext, statement.SQL, statement.Arguments...)
		if err != nil {
			status, code := databaseFailure(timeoutContext)
			rollbackError := transaction.Rollback()
			if finalError := finalizeExecution(ctx, proxy.lifecycle, executionID, status, code, joinCleanup(rollbackError, cleanup), proxy.now); finalError != nil {
				return PostgreSQLResult{}, finalError
			}
			return PostgreSQLResult{}, &PublicError{Code: code}
		}
		count, err := result.RowsAffected()
		if err != nil {
			rollbackError := transaction.Rollback()
			if finalError := finalizeExecution(ctx, proxy.lifecycle, executionID, domain.ExecutionFailed, "TARGET_RESULT_INVALID", joinCleanup(rollbackError, cleanup), proxy.now); finalError != nil {
				return PostgreSQLResult{}, finalError
			}
			return PostgreSQLResult{}, &PublicError{Code: "TARGET_RESULT_INVALID"}
		}
		rows = append(rows, count)
	}
	if err := transaction.Commit(); err != nil {
		if finalError := finalizeExecution(ctx, proxy.lifecycle, executionID, domain.ExecutionFailed, "TARGET_COMMIT_FAILED", cleanup, proxy.now); finalError != nil {
			return PostgreSQLResult{}, finalError
		}
		return PostgreSQLResult{}, &PublicError{Code: "TARGET_COMMIT_FAILED"}
	}
	if err := finalizeExecution(ctx, proxy.lifecycle, executionID, domain.ExecutionSucceeded, "", cleanup, proxy.now); err != nil {
		return PostgreSQLResult{}, err
	}
	return PostgreSQLResult{RowsAffected: rows}, nil
}

func joinCleanup(prior error, cleanup Cleanup) Cleanup {
	return func(ctx context.Context) error { return errors.Join(prior, cleanup(ctx)) }
}

func databaseFailure(ctx context.Context) (domain.ExecutionStatus, string) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return domain.ExecutionTimedOut, "TARGET_TIMEOUT"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return domain.ExecutionCancelled, "TARGET_CANCELLED"
	}
	return domain.ExecutionFailed, "TARGET_OPERATION_FAILED"
}
