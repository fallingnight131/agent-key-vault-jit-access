package store

import (
	"context"
	"strings"
	"testing"
)

type fakeMigrationStore struct {
	applied map[string]string
	calls   []string
}

func (store *fakeMigrationStore) AppliedMigrations(context.Context) (map[string]string, error) {
	result := make(map[string]string, len(store.applied))
	for version, checksum := range store.applied {
		result[version] = checksum
	}
	return result, nil
}

func (store *fakeMigrationStore) ApplyMigration(_ context.Context, migration Migration) error {
	store.calls = append(store.calls, migration.Version)
	store.applied[migration.Version] = migration.Checksum
	return nil
}

func TestMigrateIsRepeatable(t *testing.T) {
	store := &fakeMigrationStore{applied: make(map[string]string)}
	migrations, err := LoadMigrations()
	if err != nil {
		t.Fatalf("LoadMigrations() error = %v", err)
	}

	if err := Migrate(context.Background(), store); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}
	if err := Migrate(context.Background(), store); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	if len(store.calls) != len(migrations) {
		t.Fatalf("ApplyMigration calls = %v, want each migration exactly once", store.calls)
	}
}

func TestMigrateRejectsChangedHistory(t *testing.T) {
	migrations, err := LoadMigrations()
	if err != nil {
		t.Fatalf("LoadMigrations() error = %v", err)
	}
	store := &fakeMigrationStore{applied: map[string]string{migrations[0].Version: "different"}}

	err = Migrate(context.Background(), store)
	if err == nil || !strings.Contains(err.Error(), "checksum changed") {
		t.Fatalf("Migrate() error = %v, want checksum error", err)
	}
}

func TestInitialSchemaExcludesSecretMaterial(t *testing.T) {
	migrations, err := LoadMigrations()
	if err != nil {
		t.Fatalf("LoadMigrations() error = %v", err)
	}
	schema := strings.ToLower(migrations[0].SQL)
	for _, forbiddenColumn := range []string{"api_key text", "access_token text", "private_key text", "credential_secret", "agent_token text"} {
		if strings.Contains(schema, forbiddenColumn) {
			t.Errorf("schema contains forbidden secret column pattern %q", forbiddenColumn)
		}
	}
	for _, required := range []string{"password_hash", "token_hash", "vault_path", "operation_hash", "audit_events"} {
		if !strings.Contains(schema, required) {
			t.Errorf("schema missing required non-secret field or table %q", required)
		}
	}
}
