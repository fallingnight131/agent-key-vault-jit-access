package proxy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/vault"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type PGXConnectionFactory struct{}

func (PGXConnectionFactory) Connect(ctx context.Context, target catalog.ConnectionConfig, values map[string]*vault.SensitiveValue) (SQLDatabase, error) {
	if target.Host == "" || target.Port == 0 || target.Database == "" ||
		!slices.Contains([]string{"disable", "require", "verify-ca", "verify-full"}, target.TLSMode) ||
		values["username"] == nil || values["password"] == nil {
		return nil, errors.New("invalid PostgreSQL execution configuration")
	}
	var username, password string
	if err := values["username"].WithBytes(func(value []byte) error { username = string(value); return nil }); err != nil {
		return nil, errors.New("PostgreSQL credential unavailable")
	}
	if err := values["password"].WithBytes(func(value []byte) error { password = string(value); return nil }); err != nil {
		username = ""
		return nil, errors.New("PostgreSQL credential unavailable")
	}
	defer func() { username, password = "", "" }()
	configuration, err := pgx.ParseConfig(fmt.Sprintf(
		"host='%s' port=%d dbname='%s' sslmode=%s",
		escapePGXValue(target.Host), target.Port, escapePGXValue(target.Database), target.TLSMode,
	))
	if err != nil {
		return nil, errors.New("invalid PostgreSQL execution configuration")
	}
	configuration.User, configuration.Password = username, password
	connection, err := pgx.ConnectConfig(ctx, configuration)
	configuration.Password = ""
	if err != nil {
		return nil, errors.New("PostgreSQL target unavailable")
	}
	return &pgxDatabase{connection: connection}, nil
}

func escapePGXValue(value string) string {
	return strings.ReplaceAll(value, "'", "\\'")
}

type pgxDatabase struct{ connection *pgx.Conn }

func (database *pgxDatabase) ExecContext(ctx context.Context, query string, arguments ...any) (SQLResult, error) {
	tag, err := database.connection.Exec(ctx, query, arguments...)
	return pgxResult{tag}, err
}

func (database *pgxDatabase) BeginTx(ctx context.Context) (SQLTransaction, error) {
	transaction, err := database.connection.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &pgxTransaction{transaction: transaction}, nil
}

func (database *pgxDatabase) Close() error {
	return database.connection.Close(context.Background())
}

type pgxTransaction struct{ transaction pgx.Tx }

func (transaction *pgxTransaction) ExecContext(ctx context.Context, query string, arguments ...any) (SQLResult, error) {
	tag, err := transaction.transaction.Exec(ctx, query, arguments...)
	return pgxResult{tag}, err
}
func (transaction *pgxTransaction) Commit() error {
	return transaction.transaction.Commit(context.Background())
}
func (transaction *pgxTransaction) Rollback() error {
	return transaction.transaction.Rollback(context.Background())
}

type pgxResult struct{ tag pgconn.CommandTag }

func (result pgxResult) RowsAffected() (int64, error) { return result.tag.RowsAffected(), nil }
