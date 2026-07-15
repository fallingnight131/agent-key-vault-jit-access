package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/identity"
	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
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
	const ordinaryUserID = "00000000-0000-4000-8000-000000000077"
	if _, err := database.ExecContext(ctx, `INSERT INTO users (id,username,password_hash,status) VALUES ($1,'existing-user','fixture-hash','ACTIVE')`, ordinaryUserID); err != nil {
		t.Fatalf("seed existing user: %v", err)
	}
	users, err := service.ListUsers(ctx, admin)
	if err != nil || len(users) != 2 {
		t.Fatalf("ListUsers() users=%+v error=%v", users, err)
	}
	if err := service.UpdateUser(ctx, admin, ordinaryUserID, false, true); err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}
	if err := service.UpdateUser(ctx, admin, admin.ID, false, false); !errors.Is(err, identity.ErrInvalidInput) {
		t.Fatalf("admin UpdateUser() error = %v", err)
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

func TestPostgreSQLRegistrationRequiresAdminAndCreatesActiveSession(t *testing.T) {
	database := openIdentityTestDatabase(t)
	defer database.Close()
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, `TRUNCATE targets, users CASCADE`); err != nil {
		t.Fatalf("truncate database: %v", err)
	}
	service, err := identity.NewService(NewPostgreSQLIdentityRepository(database))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.Register(ctx, "new-user", "password", time.Hour); !errors.Is(err, identity.ErrRegistrationUnavailable) {
		t.Fatalf("uninitialized Register() error = %v", err)
	}
	var count int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("users after rejected registration count=%d error=%v", count, err)
	}
	if _, err := service.BootstrapAdmin(ctx, "admin", "correct horse battery staple"); err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}
	session, err := service.Register(ctx, " new-user ", "password", time.Hour)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	var passwordHash, status string
	var isAdmin, approveAll bool
	if err := database.QueryRowContext(ctx, `SELECT password_hash,is_admin,approve_all,status FROM users WHERE id=$1`, session.User.ID).Scan(&passwordHash, &isAdmin, &approveAll, &status); err != nil {
		t.Fatalf("read registered user: %v", err)
	}
	if isAdmin || approveAll || status != "ACTIVE" || session.User.Username != "new-user" || !session.User.OwnerActive {
		t.Fatalf("registered user=%+v is_admin=%t approve_all=%t status=%s", session.User, isAdmin, approveAll, status)
	}
	if passwordHash == "password" || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte("password")) != nil {
		t.Fatal("registered password was not stored as a valid bcrypt hash")
	}
	var tokenHash []byte
	if err := database.QueryRowContext(ctx, `SELECT token_hash FROM web_sessions WHERE user_id=$1`, session.User.ID).Scan(&tokenHash); err != nil {
		t.Fatalf("read registration session: %v", err)
	}
	if string(tokenHash) == session.Token || string(tokenHash) == session.CSRFToken {
		t.Fatal("registration stored a raw session or CSRF token")
	}
	if user, err := service.AuthenticateSession(ctx, session.Token); err != nil || user.ID != session.User.ID {
		t.Fatalf("AuthenticateSession() user=%+v error=%v", user, err)
	}
	var auditCount int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE event_type='users.insert' AND actor_type='USER' AND actor_id=$1`, session.User.ID).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("registration audit count=%d error=%v", auditCount, err)
	}
	var auditMetadata []byte
	if err := database.QueryRowContext(ctx, `SELECT metadata FROM audit_events WHERE event_type='users.insert' AND actor_type='USER' AND actor_id=$1`, session.User.ID).Scan(&auditMetadata); err != nil {
		t.Fatalf("read registration audit metadata: %v", err)
	}
	var metadata map[string]string
	if err := json.Unmarshal(auditMetadata, &metadata); err != nil {
		t.Fatalf("decode registration audit metadata: %v", err)
	}
	if len(metadata) != 1 || metadata["status"] != "ACTIVE" {
		t.Fatalf("registration audit metadata=%v", metadata)
	}
	if _, err := service.Register(ctx, "new-user", "different-password", time.Hour); !errors.Is(err, identity.ErrUsernameUnavailable) {
		t.Fatalf("duplicate Register() error = %v", err)
	}
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE username='new-user'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("registered user count=%d error=%v", count, err)
	}
}

func TestPostgreSQLRegistrationRollsBackWhenSessionInsertFails(t *testing.T) {
	database := openIdentityTestDatabase(t)
	defer database.Close()
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, `TRUNCATE targets, users CASCADE`); err != nil {
		t.Fatalf("truncate database: %v", err)
	}
	repository := NewPostgreSQLIdentityRepository(database)
	service, err := identity.NewService(repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	admin, err := service.BootstrapAdmin(ctx, "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}

	const existingSessionID = "00000000-0000-4000-8000-000000000091"
	now := time.Now().UTC()
	existingHash := sha256.Sum256([]byte("existing-session-fixture"))
	if _, err := database.ExecContext(ctx, `INSERT INTO web_sessions (id,user_id,token_hash,expires_at,created_at) VALUES ($1,$2,$3,$4,$5)`, existingSessionID, admin.ID, existingHash[:], now.Add(time.Hour), now); err != nil {
		t.Fatalf("seed conflicting session: %v", err)
	}

	const accountID = "00000000-0000-4000-8000-000000000092"
	newHash := sha256.Sum256([]byte("new-session-fixture"))
	err = repository.CreateAccountAndSession(ctx, identity.AccountRecord{
		ID: accountID, Username: "rollback-user", PasswordHash: []byte("fixture-hash"),
		Active: true, CreatedAt: now,
	}, identity.SessionRecord{
		ID: existingSessionID, UserID: accountID, TokenHash: newHash,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	if err == nil {
		t.Fatal("CreateAccountAndSession() unexpectedly succeeded")
	}

	var users, sessions, audits int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id=$1 OR username='rollback-user'`, accountID).Scan(&users); err != nil {
		t.Fatalf("count rolled-back users: %v", err)
	}
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM web_sessions`).Scan(&sessions); err != nil {
		t.Fatalf("count sessions after rollback: %v", err)
	}
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE event_type='users.insert' AND actor_id=$1`, accountID).Scan(&audits); err != nil {
		t.Fatalf("count registration audits after rollback: %v", err)
	}
	if users != 0 || sessions != 1 || audits != 0 {
		t.Fatalf("after rollback users=%d sessions=%d audits=%d", users, sessions, audits)
	}
}

func TestPostgreSQLConcurrentRegistrationAllowsOneUsername(t *testing.T) {
	database := openIdentityTestDatabase(t)
	defer database.Close()
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, `TRUNCATE targets, users CASCADE`); err != nil {
		t.Fatalf("truncate database: %v", err)
	}
	service, err := identity.NewService(NewPostgreSQLIdentityRepository(database))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.BootstrapAdmin(ctx, "admin", "correct horse battery staple"); err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}

	const attempts = 4
	type registrationResult struct {
		session identity.Session
		err     error
	}
	start := make(chan struct{})
	results := make(chan registrationResult, attempts)
	for range attempts {
		go func() {
			<-start
			session, err := service.Register(ctx, "concurrent-user", "password", time.Hour)
			results <- registrationResult{session: session, err: err}
		}()
	}
	close(start)

	var succeeded identity.Session
	successes, conflicts := 0, 0
	for range attempts {
		result := <-results
		switch {
		case result.err == nil:
			successes++
			succeeded = result.session
		case errors.Is(result.err, identity.ErrUsernameUnavailable):
			conflicts++
		default:
			t.Fatalf("Register() unexpected error = %v", result.err)
		}
	}
	if successes != 1 || conflicts != attempts-1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	var users, sessions int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE username='concurrent-user'`).Scan(&users); err != nil {
		t.Fatalf("count registered users: %v", err)
	}
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM web_sessions s JOIN users u ON u.id=s.user_id WHERE u.username='concurrent-user'`).Scan(&sessions); err != nil {
		t.Fatalf("count registration sessions: %v", err)
	}
	if users != 1 || sessions != 1 {
		t.Fatalf("registered users=%d sessions=%d", users, sessions)
	}
	if user, err := service.AuthenticateSession(ctx, succeeded.Token); err != nil || user.ID != succeeded.User.ID {
		t.Fatalf("AuthenticateSession() user=%+v error=%v", user, err)
	}
}

func openIdentityTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("AKV_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AKV_TEST_POSTGRES_DSN is not set")
	}
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	return database
}
