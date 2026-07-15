package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type fakeRepository struct {
	account *AccountRecord
	session *SessionRecord
}

func (repository *fakeRepository) CreateInitialAdmin(_ context.Context, account AccountRecord) error {
	if repository.account != nil {
		return ErrAlreadyInitialized
	}
	repository.account = &account
	return nil
}

func (repository *fakeRepository) FindActiveAccountByUsername(_ context.Context, username string) (AccountRecord, error) {
	if repository.account == nil || repository.account.Username != username || !repository.account.Active {
		return AccountRecord{}, ErrNotFound
	}
	return *repository.account, nil
}

func (repository *fakeRepository) CreateSession(_ context.Context, session SessionRecord) error {
	repository.session = &session
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

func TestLoginStoresOnlyTokenHash(t *testing.T) {
	repository := &fakeRepository{}
	service := newTestService(t, repository)
	if _, err := service.BootstrapAdmin(context.Background(), "admin", "password"); err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}

	session, err := service.Login(context.Background(), "admin", "password", time.Hour)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if session.Token != "raw-session-token" {
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

func newTestService(t *testing.T, repository Repository) *Service {
	t.Helper()
	service, err := NewService(repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	service.now = func() time.Time { return time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC) }
	service.newID = func() (string, error) { return "00000000-0000-4000-8000-000000000001", nil }
	service.newToken = func() (string, error) { return "raw-session-token", nil }
	return service
}
