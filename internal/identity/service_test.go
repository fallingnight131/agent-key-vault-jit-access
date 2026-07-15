package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type fakeRepository struct {
	account           *AccountRecord
	registeredAccount *AccountRecord
	session           *SessionRecord
	revoked           bool
}

func (repository *fakeRepository) CreateInitialAdmin(_ context.Context, account AccountRecord) error {
	if repository.account != nil {
		return ErrAlreadyInitialized
	}
	repository.account = &account
	return nil
}

func (repository *fakeRepository) CreateAccountAndSession(_ context.Context, account AccountRecord, session SessionRecord) error {
	if repository.account == nil || !repository.account.IsAdmin || !repository.account.Active {
		return ErrRegistrationUnavailable
	}
	if repository.account.Username == account.Username || repository.registeredAccount != nil {
		return ErrUsernameUnavailable
	}
	repository.registeredAccount = &account
	repository.session = &session
	return nil
}

func (repository *fakeRepository) FindActiveAccountByUsername(_ context.Context, username string) (AccountRecord, error) {
	for _, account := range []*AccountRecord{repository.account, repository.registeredAccount} {
		if account != nil && account.Username == username && account.Active {
			return *account, nil
		}
	}
	return AccountRecord{}, ErrNotFound
}

func (repository *fakeRepository) CreateSession(_ context.Context, session SessionRecord) error {
	repository.session = &session
	return nil
}

func (repository *fakeRepository) FindActiveSessionByTokenHash(_ context.Context, hash [sha256.Size]byte, now time.Time) (User, error) {
	if repository.session == nil || repository.session.TokenHash != hash || repository.revoked || !repository.session.ExpiresAt.After(now) {
		return User{}, ErrNotFound
	}
	for _, account := range []*AccountRecord{repository.account, repository.registeredAccount} {
		if account != nil && account.ID == repository.session.UserID && account.Active {
			return publicUser(*account), nil
		}
	}
	return User{}, ErrNotFound
}

func (repository *fakeRepository) RevokeSession(_ context.Context, hash [sha256.Size]byte, _ time.Time) error {
	if repository.session == nil || repository.session.TokenHash != hash || repository.revoked {
		return ErrNotFound
	}
	repository.revoked = true
	return nil
}

func (repository *fakeRepository) ListUsers(context.Context) ([]User, error) {
	users := make([]User, 0, 2)
	for _, account := range []*AccountRecord{repository.account, repository.registeredAccount} {
		if account != nil {
			users = append(users, publicUser(*account))
		}
	}
	return users, nil
}

func (repository *fakeRepository) UpdateNonAdminUser(_ context.Context, userID string, active, approveAll bool, _ time.Time) error {
	if repository.registeredAccount == nil || repository.registeredAccount.ID != userID {
		return ErrNotFound
	}
	repository.registeredAccount.Active, repository.registeredAccount.ApproveAll = active, approveAll
	return nil
}

func TestBootstrapAdminHashesPasswordAndIsUnique(t *testing.T) {
	repository := &fakeRepository{}
	service := newTestService(t, repository)

	user, err := service.BootstrapAdmin(context.Background(), " admin ", "correct horse")
	if err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}
	if !user.IsAdmin || user.Username != "admin" {
		t.Fatalf("user = %+v", user)
	}
	if string(repository.account.PasswordHash) == "correct horse" {
		t.Fatal("repository received plaintext password")
	}
	if err := bcrypt.CompareHashAndPassword(repository.account.PasswordHash, []byte("correct horse")); err != nil {
		t.Fatalf("stored password hash does not verify: %v", err)
	}
	if _, err := service.BootstrapAdmin(context.Background(), "second", "password"); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second BootstrapAdmin() error = %v", err)
	}
}

func TestRegisterCreatesActiveNonAdminSessionWithoutPersistingSecrets(t *testing.T) {
	repository := &fakeRepository{}
	service := newTestService(t, repository)
	if _, err := service.BootstrapAdmin(context.Background(), "admin", "password"); err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}

	session, err := service.Register(context.Background(), " new-user ", "password", time.Hour)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	account := repository.registeredAccount
	if account == nil || account.Username != "new-user" || account.IsAdmin || account.ApproveAll || !account.Active {
		t.Fatalf("registered account = %+v", account)
	}
	if string(account.PasswordHash) == "password" {
		t.Fatal("repository received plaintext password")
	}
	if err := bcrypt.CompareHashAndPassword(account.PasswordHash, []byte("password")); err != nil {
		t.Fatalf("stored password hash does not verify: %v", err)
	}
	if session.User.ID != account.ID || session.User.Username != "new-user" || session.User.IsAdmin || session.User.ApproveAll || !session.User.OwnerActive {
		t.Fatalf("session user = %+v", session.User)
	}
	if repository.session == nil || repository.session.UserID != account.ID || repository.session.TokenHash != sha256.Sum256([]byte(session.Token)) {
		t.Fatalf("stored session = %+v", repository.session)
	}
	if string(repository.session.TokenHash[:]) == session.Token || session.CSRFToken == "" {
		t.Fatal("registration persisted or omitted a raw session secret")
	}
	if user, err := service.AuthenticateSession(context.Background(), session.Token); err != nil || user.ID != account.ID {
		t.Fatalf("AuthenticateSession() user=%+v error=%v", user, err)
	}
}

func TestRegisterRequiresInitializedAdminAndUniqueUsername(t *testing.T) {
	repository := &fakeRepository{}
	service := newTestService(t, repository)
	if _, err := service.Register(context.Background(), "new-user", "password", time.Hour); !errors.Is(err, ErrRegistrationUnavailable) {
		t.Fatalf("uninitialized Register() error = %v", err)
	}
	if repository.registeredAccount != nil || repository.session != nil {
		t.Fatal("unavailable registration persisted state")
	}
	if _, err := service.BootstrapAdmin(context.Background(), "admin", "password"); err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}
	if _, err := service.Register(context.Background(), "new-user", "password", time.Hour); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := service.Register(context.Background(), "new-user", "password", time.Hour); !errors.Is(err, ErrUsernameUnavailable) {
		t.Fatalf("duplicate Register() error = %v", err)
	}
}

func TestRegisterValidatesUsernamePasswordAndLifetime(t *testing.T) {
	for name, fixture := range map[string]struct {
		username string
		password string
		lifetime time.Duration
	}{
		"empty username":         {username: "   ", password: "password", lifetime: time.Hour},
		"long username":          {username: strings.Repeat("u", 65), password: "password", lifetime: time.Hour},
		"short password":         {username: "user", password: "1234567", lifetime: time.Hour},
		"short Unicode password": {username: "user", password: "密码密码密码", lifetime: time.Hour},
		"long password":          {username: "user", password: strings.Repeat("p", 73), lifetime: time.Hour},
		"long password bytes":    {username: "user", password: strings.Repeat("密", 25), lifetime: time.Hour},
		"zero lifetime":          {username: "user", password: "password", lifetime: 0},
	} {
		t.Run(name, func(t *testing.T) {
			repository := &fakeRepository{}
			service := newTestService(t, repository)
			if _, err := service.Register(context.Background(), fixture.username, fixture.password, fixture.lifetime); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Register() error = %v", err)
			}
			if repository.registeredAccount != nil || repository.session != nil {
				t.Fatal("invalid registration persisted state")
			}
		})
	}
}

func TestLoginStoresOnlyTokenHash(t *testing.T) {
	repository := &fakeRepository{}
	service := newTestService(t, repository)
	if _, err := service.BootstrapAdmin(context.Background(), "admin", "password"); err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}

	session, err := service.Login(context.Background(), " admin ", "password", time.Hour)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if session.Token != "raw-session-token" || session.CSRFToken != "raw-session-token" {
		t.Fatalf("Token = %q", session.Token)
	}
	wantHash := sha256.Sum256([]byte(session.Token))
	if repository.session.TokenHash != wantHash {
		t.Fatalf("stored token hash = %x, want %x", repository.session.TokenHash, wantHash)
	}
	if repository.session.ExpiresAt.Sub(repository.session.CreatedAt) != time.Hour {
		t.Fatalf("session lifetime = %v", repository.session.ExpiresAt.Sub(repository.session.CreatedAt))
	}
}

func TestSessionAuthenticationAndLogout(t *testing.T) {
	repository := &fakeRepository{}
	service := newTestService(t, repository)
	if _, err := service.BootstrapAdmin(context.Background(), "admin", "password"); err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}
	session, err := service.Login(context.Background(), "admin", "password", time.Hour)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if user, err := service.AuthenticateSession(context.Background(), session.Token); err != nil || !user.IsAdmin {
		t.Fatalf("AuthenticateSession() user=%+v error=%v", user, err)
	}
	if err := service.Logout(context.Background(), session.Token); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if _, err := service.AuthenticateSession(context.Background(), session.Token); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("revoked AuthenticateSession() error = %v", err)
	}
}

func TestLoginUsesGenericInvalidCredentials(t *testing.T) {
	repository := &fakeRepository{}
	service := newTestService(t, repository)
	if _, err := service.BootstrapAdmin(context.Background(), "admin", "password"); err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}

	for _, test := range []struct{ username, password string }{
		{"admin", "wrong"},
		{"missing", "wrong"},
	} {
		if _, err := service.Login(context.Background(), test.username, test.password, time.Hour); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("Login(%q) error = %v", test.username, err)
		}
	}
	if repository.session != nil {
		t.Fatal("invalid login created a session")
	}
}

func TestUserPermissions(t *testing.T) {
	tests := []struct {
		name         string
		user         User
		agentOwnerID string
		approve      bool
		manage       bool
	}{
		{"owner", User{ID: "owner", OwnerActive: true}, "owner", true, false},
		{"other", User{ID: "other", OwnerActive: true}, "owner", false, false},
		{"approve all", User{ID: "other", ApproveAll: true, OwnerActive: true}, "owner", true, false},
		{"admin", User{ID: "admin", IsAdmin: true, OwnerActive: true}, "owner", true, true},
		{"disabled admin", User{ID: "admin", IsAdmin: true}, "owner", false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.user.CanApprove(test.agentOwnerID); got != test.approve {
				t.Errorf("CanApprove() = %t, want %t", got, test.approve)
			}
			if got := test.user.CanManageUsersAndTargets(); got != test.manage {
				t.Errorf("CanManageUsersAndTargets() = %t, want %t", got, test.manage)
			}
		})
	}
}

func TestUserManagementRequiresAdminAndPreservesAdmin(t *testing.T) {
	repository := &fakeRepository{}
	service := newTestService(t, repository)
	admin, err := service.BootstrapAdmin(context.Background(), "admin", "password")
	if err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}
	if users, err := service.ListUsers(context.Background(), admin); err != nil || len(users) != 1 {
		t.Fatalf("ListUsers() users=%+v error=%v", users, err)
	}
	if _, err := service.ListUsers(context.Background(), User{ID: "user", OwnerActive: true}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("non-admin ListUsers() error = %v", err)
	}
	if err := service.UpdateUser(context.Background(), admin, admin.ID, false, false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("admin UpdateUser() error = %v", err)
	}
}

func newTestService(t *testing.T, repository Repository) *Service {
	t.Helper()
	service, err := NewService(repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	service.now = func() time.Time { return time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC) }
	var id int
	service.newID = func() (string, error) {
		id++
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", id), nil
	}
	service.newToken = func() (string, error) { return "raw-session-token", nil }
	return service
}
