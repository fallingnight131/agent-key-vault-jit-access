package proxy

import (
	"context"
	"os"
	"testing"

	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/vault"
)

func TestPGXFactoryRejectsMissingCredentialBeforeConnecting(t *testing.T) {
	_, err := (PGXConnectionFactory{}).Connect(context.Background(), catalog.ConnectionConfig{
		Host: "db.internal", Port: 5432, Database: "app", TLSMode: "require",
	}, map[string]*vault.SensitiveValue{"username": vault.NewSensitiveValue([]byte("fixture-user"))})
	if err == nil {
		t.Fatal("Connect() error = nil")
	}
}

func TestPGXFactoryConnectsToTemporaryPostgreSQL(t *testing.T) {
	socket := os.Getenv("AKV_TEST_POSTGRES_SOCKET")
	if socket == "" {
		t.Skip("AKV_TEST_POSTGRES_SOCKET is not set")
	}
	database, err := (PGXConnectionFactory{}).Connect(context.Background(), catalog.ConnectionConfig{
		Host: socket, Port: 5432, Database: "akvtest", TLSMode: "disable",
	}, map[string]*vault.SensitiveValue{
		"username": vault.NewSensitiveValue([]byte("akvtest")),
		"password": vault.NewSensitiveValue([]byte("fixture-password")),
	})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer database.Close()
	result, err := database.ExecContext(context.Background(), "CREATE TEMP TABLE connector_probe (id integer)")
	if err != nil {
		t.Fatalf("ExecContext() error = %v", err)
	}
	if rows, _ := result.RowsAffected(); rows != 0 {
		t.Fatalf("RowsAffected() = %d", rows)
	}
}
