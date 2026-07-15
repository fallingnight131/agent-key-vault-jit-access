package proxy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/vault"
)

type fakeSQLResult int64

func (result fakeSQLResult) RowsAffected() (int64, error) { return int64(result), nil }

type fakeSQLDatabase struct {
	transaction *fakeSQLTransaction
	closed      bool
}

func (*fakeSQLDatabase) ExecContext(context.Context, string, ...any) (SQLResult, error) {
	return fakeSQLResult(1), nil
}
func (database *fakeSQLDatabase) BeginTx(context.Context) (SQLTransaction, error) {
	return database.transaction, nil
}
func (database *fakeSQLDatabase) Close() error { database.closed = true; return nil }

type fakeSQLTransaction struct {
	executions int
	failAt     int
	committed  bool
	rolledBack bool
}

func (transaction *fakeSQLTransaction) ExecContext(context.Context, string, ...any) (SQLResult, error) {
	transaction.executions++
	if transaction.executions == transaction.failAt {
		return nil, errors.New("fixture statement failure")
	}
	return fakeSQLResult(1), nil
}
func (transaction *fakeSQLTransaction) Commit() error   { transaction.committed = true; return nil }
func (transaction *fakeSQLTransaction) Rollback() error { transaction.rolledBack = true; return nil }

type fakeSQLFactory struct {
	calls    int
	database *fakeSQLDatabase
}

func (factory *fakeSQLFactory) Connect(context.Context, catalog.ConnectionConfig, map[string]*vault.SensitiveValue) (SQLDatabase, error) {
	factory.calls++
	return factory.database, nil
}

type dynamicVault struct {
	issueError  error
	issueCalls  int
	revokeCalls int
}

func (*dynamicVault) ReadKV(context.Context, string, *int) (map[string]*vault.SensitiveValue, error) {
	return nil, errors.New("static fallback forbidden")
}
func (*dynamicVault) Sign(context.Context, string, string, []byte) ([]byte, error) { return nil, nil }
func (client *dynamicVault) IssueDatabase(context.Context, string, time.Duration) (vault.DynamicCredential, error) {
	client.issueCalls++
	if client.issueError != nil {
		return vault.DynamicCredential{}, client.issueError
	}
	return vault.DynamicCredential{
		Username: vault.NewSensitiveValue([]byte("fixture-user")), Password: vault.NewSensitiveValue([]byte("fixture-password")), LeaseID: "lease",
	}, nil
}
func (client *dynamicVault) RevokeLease(context.Context, string) error {
	client.revokeCalls++
	return nil
}

func TestPostgreSQLDynamicFailureHasNoConnectionOrFallback(t *testing.T) {
	vaultClient := &dynamicVault{issueError: errors.New("fixture issuance failed")}
	factory := &fakeSQLFactory{}
	proxy := NewPostgreSQLProxy(&fakePlans{postgresPlan()}, &fakeGuard{}, vaultClient, &fakeLifecycle{}, factory)
	_, err := proxy.Execute(context.Background(), "request", "agent", "task")
	if err == nil || vaultClient.issueCalls != 1 || factory.calls != 0 {
		t.Fatalf("error=%v issue calls=%d connect calls=%d", err, vaultClient.issueCalls, factory.calls)
	}
}

func TestPostgreSQLBatchRollsBackAndRevokesLease(t *testing.T) {
	vaultClient := &dynamicVault{}
	transaction := &fakeSQLTransaction{failAt: 2}
	database := &fakeSQLDatabase{transaction: transaction}
	factory := &fakeSQLFactory{database: database}
	lifecycle := &fakeLifecycle{}
	proxy := NewPostgreSQLProxy(&fakePlans{postgresPlan()}, &fakeGuard{}, vaultClient, lifecycle, factory)
	_, err := proxy.Execute(context.Background(), "request", "agent", "task")
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	if !transaction.rolledBack || transaction.committed || transaction.executions != 2 {
		t.Fatalf("transaction=%+v", transaction)
	}
	if vaultClient.revokeCalls != 1 || !database.closed {
		t.Fatalf("revoke calls=%d database closed=%t", vaultClient.revokeCalls, database.closed)
	}
}

func postgresPlan() Plan {
	plan := validPlan("https://unused.example.test")
	plan.Target.ConnectorType = catalog.ConnectorPostgreSQL
	plan.Target.ConnectionConfig = catalog.ConnectionConfig{Host: "db.internal", Port: 5432, Database: "app", TLSMode: "require", RequireDynamic: true}
	plan.Credential.Type = catalog.CredentialPostgreSQLDynamic
	plan.Credential.VaultPath = "database/creds/app"
	plan.Operation = authorization.Operation{
		Kind: authorization.OperationPostgreSQLBatch,
		PostgreSQL: &authorization.PostgreSQLParameters{Statements: []authorization.SQLStatement{
			{SQL: "UPDATE tickets SET state=$1 WHERE id=$2", Arguments: []any{"open", 1}},
			{SQL: "INSERT INTO audit(ticket_id) VALUES($1)", Arguments: []any{1}},
		}},
	}
	return plan
}
