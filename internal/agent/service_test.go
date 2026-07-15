package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"
)

type fakeRepository struct {
	agent Record
	token TokenRecord
}

func (repository *fakeRepository) CreateAgentWithToken(_ context.Context, agent Record, token TokenRecord) error {
	repository.agent, repository.token = agent, token
	return nil
}

func (repository *fakeRepository) ReplaceAgentToken(_ context.Context, ownerID, agentID string, token TokenRecord, revokedAt time.Time) error {
	if repository.agent.ID != agentID || repository.agent.OwnerUserID != ownerID {
		return ErrForbidden
	}
	repository.token.RevokedAt = &revokedAt
	repository.token = token
	return nil
}

func (repository *fakeRepository) FindAgentByTokenHash(_ context.Context, hash [sha256.Size]byte) (Record, TokenRecord, error) {
	if repository.token.TokenHash != hash {
		return Record{}, TokenRecord{}, ErrUnauthorized
	}
	return repository.agent, repository.token, nil
}

func (repository *fakeRepository) RevokeAgentToken(_ context.Context, ownerID, agentID string, revokedAt time.Time) error {
	if repository.agent.ID != agentID || repository.agent.OwnerUserID != ownerID {
		return ErrForbidden
	}
	repository.token.RevokedAt = &revokedAt
	return nil
}

func (repository *fakeRepository) SetAgentActive(_ context.Context, ownerID, agentID string, active bool, _ time.Time) error {
	if repository.agent.ID != agentID || repository.agent.OwnerUserID != ownerID {
		return ErrForbidden
	}
	repository.agent.Active = active
	return nil
}

func (repository *fakeRepository) ListOwnedAgents(_ context.Context, ownerID string) ([]View, error) {
	if repository.agent.OwnerUserID != ownerID {
		return nil, nil
	}
	return []View{{
		ID: repository.agent.ID, Name: repository.agent.Name, Active: repository.agent.Active,
		CreatedAt: repository.agent.CreatedAt, HasActiveToken: repository.token.ID != "" && repository.token.RevokedAt == nil,
		TokenExpiresAt: repository.token.ExpiresAt,
	}}, nil
}

func TestRegisterTokenLifetimesAndHashes(t *testing.T) {
	tests := []struct {
		lifetime  TokenLifetime
		duration  time.Duration
		permanent bool
	}{
		{Token24Hours, 24 * time.Hour, false},
		{Token30Days, 30 * 24 * time.Hour, false},
		{TokenPermanent, 0, true},
	}
	for _, test := range tests {
		t.Run(string(test.lifetime), func(t *testing.T) {
			repository := &fakeRepository{}
			service := newTestService(repository)
			credential, err := service.Register(context.Background(), "owner", "agent", test.lifetime)
			if err != nil {
				t.Fatalf("Register() error = %v", err)
			}
			if repository.token.TokenHash != sha256.Sum256([]byte(credential.Token)) {
				t.Fatal("repository did not receive token hash")
			}
			if credential.PermanentWarning != test.permanent {
				t.Fatalf("PermanentWarning = %t", credential.PermanentWarning)
			}
			if test.permanent {
				if credential.ExpiresAt != nil {
					t.Fatalf("ExpiresAt = %v, want nil", credential.ExpiresAt)
				}
			} else if credential.ExpiresAt.Sub(repository.token.CreatedAt) != test.duration {
				t.Fatalf("duration = %v, want %v", credential.ExpiresAt.Sub(repository.token.CreatedAt), test.duration)
			}
		})
	}
}

func TestRotateInvalidatesOldToken(t *testing.T) {
	repository := &fakeRepository{}
	service := newTestService(repository)
	oldCredential, err := service.Register(context.Background(), "owner", "agent", Token24Hours)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	newCredential, err := service.RotateToken(context.Background(), "owner", oldCredential.AgentID, Token30Days)
	if err != nil {
		t.Fatalf("RotateToken() error = %v", err)
	}
	if _, err := service.Authenticate(context.Background(), oldCredential.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old token Authenticate() error = %v", err)
	}
	if _, err := service.Authenticate(context.Background(), newCredential.Token); err != nil {
		t.Fatalf("new token Authenticate() error = %v", err)
	}
}

func TestAuthenticationRejectsExpiredRevokedAndDisabled(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeRepository, *Service)
	}{
		{"expired", func(repository *fakeRepository, service *Service) {
			expired := service.now().Add(-time.Second)
			repository.token.ExpiresAt = &expired
		}},
		{"revoked", func(repository *fakeRepository, service *Service) {
			revoked := service.now()
			repository.token.RevokedAt = &revoked
		}},
		{"disabled", func(repository *fakeRepository, _ *Service) { repository.agent.Active = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{}
			service := newTestService(repository)
			credential, err := service.Register(context.Background(), "owner", "agent", Token24Hours)
			if err != nil {
				t.Fatalf("Register() error = %v", err)
			}
			test.mutate(repository, service)
			if _, err := service.Authenticate(context.Background(), credential.Token); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("Authenticate() error = %v", err)
			}
		})
	}
}

func TestOwnerBoundary(t *testing.T) {
	repository := &fakeRepository{}
	service := newTestService(repository)
	credential, err := service.Register(context.Background(), "owner", "agent", Token24Hours)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := service.RotateToken(context.Background(), "other", credential.AgentID, Token24Hours); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-owner RotateToken() error = %v", err)
	}
	if err := service.RevokeToken(context.Background(), "other", credential.AgentID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-owner RevokeToken() error = %v", err)
	}
}

func TestRevokeTokenIsIdempotentForOwner(t *testing.T) {
	repository := &fakeRepository{}
	service := newTestService(repository)
	credential, err := service.Register(context.Background(), "owner", "agent", TokenPermanent)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := service.RevokeToken(context.Background(), "owner", credential.AgentID); err != nil {
		t.Fatalf("first RevokeToken() error = %v", err)
	}
	if err := service.RevokeToken(context.Background(), "owner", credential.AgentID); err != nil {
		t.Fatalf("second RevokeToken() error = %v", err)
	}
	if _, err := service.Authenticate(context.Background(), credential.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked Authenticate() error = %v", err)
	}
	records, err := service.List(context.Background(), "owner")
	if err != nil || len(records) != 1 || records[0].HasActiveToken {
		t.Fatalf("List() records=%+v error=%v", records, err)
	}
}

func newTestService(repository Repository) *Service {
	service := NewService(repository)
	service.now = func() time.Time { return time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC) }
	ids := []string{"agent-id", "token-id", "replacement-placeholder", "replacement-token-id"}
	service.newID = func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}
	tokens := []string{"first-raw-token", "second-raw-token"}
	service.newToken = func() (string, error) {
		token := tokens[0]
		tokens = tokens[1:]
		return token, nil
	}
	return service
}
