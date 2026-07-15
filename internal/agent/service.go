package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidInput = errors.New("invalid agent input")
	ErrUnauthorized = errors.New("agent unauthorized")
	ErrForbidden    = errors.New("agent operation forbidden")
)

type TokenLifetime string

const (
	Token24Hours   TokenLifetime = "24_HOURS"
	Token30Days    TokenLifetime = "30_DAYS"
	TokenPermanent TokenLifetime = "PERMANENT"
)

func (lifetime TokenLifetime) expiration(now time.Time) (*time.Time, bool) {
	var expiresAt time.Time
	switch lifetime {
	case Token24Hours:
		expiresAt = now.Add(24 * time.Hour)
	case Token30Days:
		expiresAt = now.Add(30 * 24 * time.Hour)
	case TokenPermanent:
		return nil, true
	default:
		return nil, false
	}
	return &expiresAt, true
}

type Record struct {
	ID          string
	OwnerUserID string
	Name        string
	Active      bool
	CreatedAt   time.Time
}

type TokenRecord struct {
	ID        string
	AgentID   string
	TokenHash [sha256.Size]byte
	CreatedAt time.Time
	ExpiresAt *time.Time
	RevokedAt *time.Time
}

type Principal struct {
	AgentID     string
	OwnerUserID string
}

type Credential struct {
	AgentID          string
	Token            string
	ExpiresAt        *time.Time
	PermanentWarning bool
}

type Repository interface {
	CreateAgentWithToken(context.Context, Record, TokenRecord) error
	ReplaceAgentToken(context.Context, string, string, TokenRecord, time.Time) error
	FindAgentByTokenHash(context.Context, [sha256.Size]byte) (Record, TokenRecord, error)
	RevokeAgentToken(context.Context, string, string, time.Time) error
	SetAgentActive(context.Context, string, string, bool, time.Time) error
}

type Service struct {
	repository Repository
	now        func() time.Time
	newID      func() (string, error)
	newToken   func() (string, error)
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, now: time.Now, newID: randomID, newToken: randomToken}
}

func (service *Service) Register(ctx context.Context, ownerUserID, name string, lifetime TokenLifetime) (Credential, error) {
	name = strings.TrimSpace(name)
	if ownerUserID == "" || name == "" {
		return Credential{}, ErrInvalidInput
	}
	now := service.now()
	expiresAt, ok := lifetime.expiration(now)
	if !ok {
		return Credential{}, ErrInvalidInput
	}
	agentID, tokenRecord, rawToken, err := service.newTokenRecord(now, expiresAt)
	if err != nil {
		return Credential{}, err
	}
	agentRecord := Record{ID: agentID, OwnerUserID: ownerUserID, Name: name, Active: true, CreatedAt: now}
	tokenRecord.AgentID = agentID
	if err := service.repository.CreateAgentWithToken(ctx, agentRecord, tokenRecord); err != nil {
		return Credential{}, fmt.Errorf("create agent: %w", err)
	}
	return Credential{AgentID: agentID, Token: rawToken, ExpiresAt: expiresAt, PermanentWarning: expiresAt == nil}, nil
}

func (service *Service) RotateToken(ctx context.Context, ownerUserID, agentID string, lifetime TokenLifetime) (Credential, error) {
	if ownerUserID == "" || agentID == "" {
		return Credential{}, ErrInvalidInput
	}
	now := service.now()
	expiresAt, ok := lifetime.expiration(now)
	if !ok {
		return Credential{}, ErrInvalidInput
	}
	_, tokenRecord, rawToken, err := service.newTokenRecord(now, expiresAt)
	if err != nil {
		return Credential{}, err
	}
	tokenRecord.AgentID = agentID
	if err := service.repository.ReplaceAgentToken(ctx, ownerUserID, agentID, tokenRecord, now); err != nil {
		return Credential{}, fmt.Errorf("replace agent token: %w", err)
	}
	return Credential{AgentID: agentID, Token: rawToken, ExpiresAt: expiresAt, PermanentWarning: expiresAt == nil}, nil
}

func (service *Service) Authenticate(ctx context.Context, rawToken string) (Principal, error) {
	if rawToken == "" {
		return Principal{}, ErrUnauthorized
	}
	record, token, err := service.repository.FindAgentByTokenHash(ctx, sha256.Sum256([]byte(rawToken)))
	if err != nil || !record.Active || token.RevokedAt != nil || (token.ExpiresAt != nil && !token.ExpiresAt.After(service.now())) {
		return Principal{}, ErrUnauthorized
	}
	return Principal{AgentID: record.ID, OwnerUserID: record.OwnerUserID}, nil
}

func (service *Service) RevokeToken(ctx context.Context, ownerUserID, agentID string) error {
	if ownerUserID == "" || agentID == "" {
		return ErrInvalidInput
	}
	if err := service.repository.RevokeAgentToken(ctx, ownerUserID, agentID, service.now()); err != nil {
		return fmt.Errorf("revoke agent token: %w", err)
	}
	return nil
}

func (service *Service) SetActive(ctx context.Context, ownerUserID, agentID string, active bool) error {
	if ownerUserID == "" || agentID == "" {
		return ErrInvalidInput
	}
	if err := service.repository.SetAgentActive(ctx, ownerUserID, agentID, active, service.now()); err != nil {
		return fmt.Errorf("set agent active: %w", err)
	}
	return nil
}

func (service *Service) newTokenRecord(now time.Time, expiresAt *time.Time) (string, TokenRecord, string, error) {
	agentOrTokenID, err := service.newID()
	if err != nil {
		return "", TokenRecord{}, "", fmt.Errorf("generate ID: %w", err)
	}
	tokenID, err := service.newID()
	if err != nil {
		return "", TokenRecord{}, "", fmt.Errorf("generate token ID: %w", err)
	}
	rawToken, err := service.newToken()
	if err != nil {
		return "", TokenRecord{}, "", fmt.Errorf("generate token: %w", err)
	}
	return agentOrTokenID, TokenRecord{
		ID: tokenID, TokenHash: sha256.Sum256([]byte(rawToken)), CreatedAt: now, ExpiresAt: expiresAt,
	}, rawToken, nil
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return "akv_agent_" + base64.RawURLEncoding.EncodeToString(buffer), nil
}

func randomID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buffer[0:4], buffer[4:6], buffer[6:8], buffer[8:10], buffer[10:16]), nil
}
