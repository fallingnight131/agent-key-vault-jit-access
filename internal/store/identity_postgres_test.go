package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/identity"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgreSQLWebSessionLifecycle(t *testing.T) {
	dsn := os.Getenv("AKV_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AKV_TEST_POSTGRES_DSN is not set")
	}
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer database.Close()
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, `TRUNCATE targets, users CASCADE`); err != nil {
		t.Fatalf("truncate database: %v", err)
	}
	service, err := identity.NewService(NewPostgreSQLIdentityRepository(database))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	admin, err := service.BootstrapAdmin(ctx, "admin", "correct horse battery staple")
	if err != nil || !admin.IsAdmin {
		t.Fatalf("BootstrapAdmin() admin=%+v error=%v", admin, err)
	}
	if _, err := service.BootstrapAdmin(ctx, "second", "password"); !errors.Is(err, identity.ErrAlreadyInitialized) {
		t.Fatalf("second BootstrapAdmin() error = %v", err)
	}
	session, err := service.Login(ctx, "admin", "correct horse battery staple", time.Hour)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	var storedHash []byte
	if err := database.QueryRowContext(ctx, `SELECT token_hash FROM web_sessions`).Scan(&storedHash); err != nil {
		t.Fatalf("read session hash: %v", err)
	}
	if string(storedHash) == session.Token {
		t.Fatal("database stored raw session token")
	}
	if user, err := service.AuthenticateSession(ctx, session.Token); err != nil || user.ID != admin.ID {
		t.Fatalf("AuthenticateSession() user=%+v error=%v", user, err)
	}
	if err := service.Logout(ctx, session.Token); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if _, err := service.AuthenticateSession(ctx, session.Token); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("revoked AuthenticateSession() error = %v", err)
	}

	secondSession, err := service.Login(ctx, "admin", "correct horse battery staple", time.Hour)
	if err != nil {
		t.Fatalf("second Login() error = %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE users SET status='DISABLED' WHERE id=$1`, admin.ID); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	if _, err := service.AuthenticateSession(ctx, secondSession.Token); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("disabled-user AuthenticateSession() error = %v", err)
	}
}
