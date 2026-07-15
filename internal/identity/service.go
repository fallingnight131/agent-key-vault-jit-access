package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrAlreadyInitialized      = errors.New("identity already initialized")
	ErrInvalidCredentials      = errors.New("invalid credentials")
	ErrInvalidInput            = errors.New("invalid identity input")
	ErrNotFound                = errors.New("identity not found")
	ErrRegistrationUnavailable = errors.New("registration unavailable")
	ErrUsernameUnavailable     = errors.New("username unavailable")
)

type User struct {
	ID          string
	Username    string
	IsAdmin     bool
	ApproveAll  bool
	OwnerActive bool
}

func (user User) CanApprove(agentOwnerUserID string) bool {
	return user.OwnerActive && (user.IsAdmin || user.ApproveAll || user.ID == agentOwnerUserID)
}

func (user User) CanManageUsersAndTargets() bool {
	return user.OwnerActive && user.IsAdmin
}

type AccountRecord struct {
	ID           string
	Username     string
	PasswordHash []byte
	IsAdmin      bool
	ApproveAll   bool
	Active       bool
	CreatedAt    time.Time
}

type SessionRecord struct {
	ID        string
	UserID    string
	TokenHash [sha256.Size]byte
	CreatedAt time.Time
	ExpiresAt time.Time
}

type Session struct {
	Token     string
	CSRFToken string
	ExpiresAt time.Time
	User      User
}

type Repository interface {
	CreateInitialAdmin(context.Context, AccountRecord) error
	CreateAccountAndSession(context.Context, AccountRecord, SessionRecord) error
	FindActiveAccountByUsername(context.Context, string) (AccountRecord, error)
	CreateSession(context.Context, SessionRecord) error
	FindActiveSessionByTokenHash(context.Context, [sha256.Size]byte, time.Time) (User, error)
	RevokeSession(context.Context, [sha256.Size]byte, time.Time) error
	ListUsers(context.Context) ([]User, error)
	UpdateNonAdminUser(context.Context, string, bool, bool, time.Time) error
}

func (service *Service) Register(ctx context.Context, username, password string, lifetime time.Duration) (Session, error) {
	username = strings.TrimSpace(username)
	passwordLength := len([]byte(password))
	if username == "" || !utf8.ValidString(username) || utf8.RuneCountInString(username) > 64 ||
		!utf8.ValidString(password) || utf8.RuneCountInString(password) < 8 || passwordLength > 72 || lifetime <= 0 {
		return Session{}, ErrInvalidInput
	}
	userID, err := service.newID()
	if err != nil {
		return Session{}, fmt.Errorf("generate user ID: %w", err)
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return Session{}, fmt.Errorf("hash password: %w", err)
	}
	record := AccountRecord{
		ID: userID, Username: username, PasswordHash: passwordHash,
		IsAdmin: false, ApproveAll: false, Active: true, CreatedAt: service.now(),
	}
	sessionRecord, session, err := service.prepareSession(record, lifetime)
	if err != nil {
		return Session{}, err
	}
	if err := service.repository.CreateAccountAndSession(ctx, record, sessionRecord); err != nil {
		if errors.Is(err, ErrRegistrationUnavailable) || errors.Is(err, ErrUsernameUnavailable) {
			return Session{}, err
		}
		return Session{}, fmt.Errorf("create account and session: %w", err)
	}
	return session, nil
}

type Service struct {
	repository Repository
	now        func() time.Time
	newID      func() (string, error)
	newToken   func() (string, error)
	dummyHash  []byte
}

func NewService(repository Repository) (*Service, error) {
	dummyHash, err := bcrypt.GenerateFromPassword([]byte("invalid-password-placeholder"), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("prepare password verifier: %w", err)
	}
	return &Service{
		repository: repository,
		now:        time.Now,
		newID:      randomUUID,
		newToken:   randomToken,
		dummyHash:  dummyHash,
	}, nil
}

func (service *Service) BootstrapAdmin(ctx context.Context, username, password string) (User, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return User{}, ErrInvalidInput
	}
	id, err := service.newID()
	if err != nil {
		return User{}, fmt.Errorf("generate user ID: %w", err)
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}
	record := AccountRecord{
		ID: id, Username: username, PasswordHash: passwordHash,
		IsAdmin: true, Active: true, CreatedAt: service.now(),
	}
	if err := service.repository.CreateInitialAdmin(ctx, record); err != nil {
		if errors.Is(err, ErrAlreadyInitialized) {
			return User{}, ErrAlreadyInitialized
		}
		return User{}, fmt.Errorf("create initial admin: %w", err)
	}
	return publicUser(record), nil
}

func (service *Service) Login(ctx context.Context, username, password string, lifetime time.Duration) (Session, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" || lifetime <= 0 {
		return Session{}, ErrInvalidInput
	}
	record, err := service.repository.FindActiveAccountByUsername(ctx, username)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Session{}, fmt.Errorf("find account: %w", err)
	}
	hash := record.PasswordHash
	if errors.Is(err, ErrNotFound) {
		hash = service.dummyHash
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil || errors.Is(err, ErrNotFound) {
		return Session{}, ErrInvalidCredentials
	}

	sessionRecord, session, err := service.prepareSession(record, lifetime)
	if err != nil {
		return Session{}, err
	}
	if err := service.repository.CreateSession(ctx, sessionRecord); err != nil {
		return Session{}, fmt.Errorf("create session: %w", err)
	}
	return session, nil
}

func (service *Service) prepareSession(record AccountRecord, lifetime time.Duration) (SessionRecord, Session, error) {
	id, err := service.newID()
	if err != nil {
		return SessionRecord{}, Session{}, fmt.Errorf("generate session ID: %w", err)
	}
	token, err := service.newToken()
	if err != nil {
		return SessionRecord{}, Session{}, fmt.Errorf("generate session token: %w", err)
	}
	csrfToken, err := service.newToken()
	if err != nil {
		return SessionRecord{}, Session{}, fmt.Errorf("generate CSRF token: %w", err)
	}
	now := service.now()
	sessionRecord := SessionRecord{
		ID: id, UserID: record.ID, TokenHash: sha256.Sum256([]byte(token)),
		CreatedAt: now, ExpiresAt: now.Add(lifetime),
	}
	return sessionRecord, Session{
		Token: token, CSRFToken: csrfToken, ExpiresAt: sessionRecord.ExpiresAt, User: publicUser(record),
	}, nil
}

func (service *Service) AuthenticateSession(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrInvalidCredentials
	}
	user, err := service.repository.FindActiveSessionByTokenHash(ctx, sha256.Sum256([]byte(token)), service.now())
	if err != nil || !user.OwnerActive {
		return User{}, ErrInvalidCredentials
	}
	return user, nil
}

func (service *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return ErrInvalidCredentials
	}
	if err := service.repository.RevokeSession(ctx, sha256.Sum256([]byte(token)), service.now()); err != nil {
		return ErrInvalidCredentials
	}
	return nil
}

func (service *Service) ListUsers(ctx context.Context, actor User) ([]User, error) {
	if !actor.CanManageUsersAndTargets() {
		return nil, ErrInvalidInput
	}
	users, err := service.repository.ListUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return users, nil
}

func (service *Service) UpdateUser(ctx context.Context, actor User, userID string, active, approveAll bool) error {
	if !actor.CanManageUsersAndTargets() || userID == "" {
		return ErrInvalidInput
	}
	if err := service.repository.UpdateNonAdminUser(ctx, userID, active, approveAll, service.now()); err != nil {
		return ErrInvalidInput
	}
	return nil
}

func publicUser(record AccountRecord) User {
	return User{
		ID: record.ID, Username: record.Username, IsAdmin: record.IsAdmin,
		ApproveAll: record.ApproveAll, OwnerActive: record.Active,
	}
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func randomUUID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buffer[0:4], buffer[4:6], buffer[6:8], buffer[8:10], buffer[10:16]), nil
}
