package store

import (
	"context"
	"database/sql"
	"fmt"
)

// PostgreSQLMigrationStore stores migration history in the AKV database.
type PostgreSQLMigrationStore struct {
	database *sql.DB
}

func NewPostgreSQLMigrationStore(database *sql.DB) *PostgreSQLMigrationStore {
	return &PostgreSQLMigrationStore{database: database}
}

func (store *PostgreSQLMigrationStore) AppliedMigrations(ctx context.Context) (map[string]string, error) {
	if _, err := store.database.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version text PRIMARY KEY,
    checksum text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
)`); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	rows, err := store.database.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]string)
	for rows.Next() {
		var version, checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[version] = checksum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema_migrations: %w", err)
	}
	return applied, nil
}

func (store *PostgreSQLMigrationStore) ApplyMigration(ctx context.Context, migration Migration) error {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	if _, err := transaction.ExecContext(ctx, migration.SQL); err != nil {
		return fmt.Errorf("execute SQL: %w", err)
	}
	if _, err := transaction.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, checksum) VALUES ($1, $2)`,
		migration.Version, migration.Checksum,
	); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
