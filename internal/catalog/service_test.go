package catalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/identity"
	"github.com/fallingnight/akv/internal/vault"
)

type fakeRepository struct {
	target     Target
	credential Credential
}

func (repository *fakeRepository) CreateTargetWithDefaultCredential(_ context.Context, target Target, credential Credential) error {
	repository.target, repository.credential = target, credential
	return nil
}

func (repository *fakeRepository) ListActiveTargets(context.Context) ([]Target, error) {
	if !repository.target.Active {
		return nil, nil
	}
	return []Target{repository.target}, nil
}

func (repository *fakeRepository) FindActiveTargetAndDefaultCredential(_ context.Context, targetID string) (Target, Credential, error) {
	if repository.target.ID != targetID {
		return Target{}, Credential{}, ErrUnavailable
	}
	return repository.target, repository.credential, nil
}
func (repository *fakeRepository) ListCatalog(context.Context) ([]Target, []Credential, error) {
	return []Target{repository.target}, []Credential{repository.credential}, nil
}
func (repository *fakeRepository) FindCredential(_ context.Context, id string) (Credential, error) {
	if repository.credential.ID != id {
		return Credential{}, ErrUnavailable
	}
	return repository.credential, nil
}
func (repository *fakeRepository) FindTargetWithDefaultCredential(_ context.Context, id string) (Target, Credential, error) {
	if repository.target.ID != id {
		return Target{}, Credential{}, ErrUnavailable
	}
	return repository.target, repository.credential, nil
}
func (repository *fakeRepository) UpdateTarget(_ context.Context, target Target, _ time.Time) error {
	repository.target = target
	return nil
}
func (repository *fakeRepository) SetTargetActive(_ context.Context, id string, active bool, _ time.Time) error {
	if repository.target.ID != id {
		return ErrUnavailable
	}
	repository.target.Active = active
	return nil
}
func (repository *fakeRepository) SetCredentialActive(_ context.Context, id string, active bool, _ time.Time) error {
	if repository.credential.ID != id {
		return ErrUnavailable
	}
	repository.credential.Active = active
	return nil
}

type fakeWriter struct {
	path  string
	calls int
}

func (writer *fakeWriter) WriteKV(_ context.Context, write vault.KVWrite) error {
	writer.path = write.Path
	writer.calls++
	return nil
}
func (writer *fakeWriter) ConfigureTransitKey(context.Context, vault.TransitKey) error {
	writer.calls++
	return nil
}
func (writer *fakeWriter) ConfigureDatabaseRole(context.Context, vault.DatabaseRole) error {
	writer.calls++
	return nil
}

func TestCreateTargetRequiresAdmin(t *testing.T) {
	service := newTestService(&fakeRepository{})
	input := validHTTPInput()
	for _, actor := range []identity.User{
		{ID: "user", OwnerActive: true},
		{ID: "approver", ApproveAll: true, OwnerActive: true},
		{ID: "disabled-admin", IsAdmin: true},
	} {
		if _, _, err := service.CreateTarget(context.Background(), actor, input); !errors.Is(err, ErrForbidden) {
			t.Errorf("CreateTarget(%+v) error = %v", actor, err)
		}
	}
}

func TestCreateAndResolveServerDefaultCredential(t *testing.T) {
	repository := &fakeRepository{}
	service := newTestService(repository)
	admin := identity.User{ID: "admin", IsAdmin: true, OwnerActive: true}

	target, credential, err := service.CreateTarget(context.Background(), admin, validHTTPInput())
	if err != nil {
		t.Fatalf("CreateTarget() error = %v", err)
	}
	resolvedTarget, resolvedCredential, err := service.ResolveForRequest(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("ResolveForRequest() error = %v", err)
	}
	if resolvedTarget.DefaultCredentialID != credential.ID || resolvedCredential.ID != credential.ID || credential.VaultProvider != "OPENBAO" {
		t.Fatalf("resolved target=%+v credential=%+v", resolvedTarget, resolvedCredential)
	}
	if _, err := service.Discover(context.Background(), "agent-id"); err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if _, err := service.Discover(context.Background(), ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("anonymous Discover() error = %v", err)
	}
}

func TestProvisionTargetGeneratesServerVaultPath(t *testing.T) {
	repository := &fakeRepository{}
	writer := &fakeWriter{}
	service := NewManagementService(repository, writer)
	ids := []string{"target-id", "credential-id"}
	service.newID = func() (string, error) { id := ids[0]; ids = ids[1:]; return id, nil }
	secret := vault.NewSensitiveValue([]byte("fixture"))
	defer secret.Destroy()
	input := validHTTPInput()
	input.VaultPath = "attacker/path"
	target, credential, err := service.ProvisionTarget(context.Background(), identity.User{ID: "admin", IsAdmin: true, OwnerActive: true}, ProvisionInput{CreateInput: input, SecretValues: map[string]*vault.SensitiveValue{"api_key": secret}})
	if err != nil || target.ID != "target-id" || credential.VaultPath != "kv/data/credentials/credential-id" || writer.path != credential.VaultPath {
		t.Fatalf("target=%+v credential=%+v path=%q error=%v", target, credential, writer.path, err)
	}
}

func TestConnectionConfigRejectsCredentialBypass(t *testing.T) {
	service := newTestService(&fakeRepository{})
	admin := identity.User{ID: "admin", IsAdmin: true, OwnerActive: true}
	tests := []CreateInput{
		func() CreateInput {
			input := validHTTPInput()
			input.ConnectionConfig.BaseURL = "https://user:password@example.test"
			return input
		}(),
		func() CreateInput {
			input := validHTTPInput()
			input.ConnectionConfig.Host = "secret-host"
			return input
		}(),
		{
			Name: "db", ConnectorType: ConnectorPostgreSQL,
			ConnectionConfig: ConnectionConfig{Host: "db.internal", Port: 5432, Database: "app", TLSMode: "require", RequireDynamic: true},
			CredentialAlias:  "fixed", CredentialType: CredentialUsernamePassword, VaultPath: "kv/data/db/fixed",
		},
	}
	for index, input := range tests {
		if _, _, err := service.CreateTarget(context.Background(), admin, input); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("case %d CreateTarget() error = %v", index, err)
		}
	}
}

func TestCertificateCanBeStoredForHTTPWithoutBecomingExecutable(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)
	_, credential, err := service.CreateTarget(context.Background(), identity.User{ID: "admin", IsAdmin: true, OwnerActive: true}, CreateInput{
		Name: "certificate archive", ConnectorType: ConnectorHTTP,
		ConnectionConfig: ConnectionConfig{BaseURL: "https://target.example.test", AllowedHTTPMethods: []string{"POST"}},
		CredentialAlias:  "stored-only", CredentialType: CredentialCertificate, VaultPath: "kv/data/certificate",
	})
	if err != nil {
		t.Fatalf("CreateTarget() error = %v", err)
	}
	if credential.Type != CredentialCertificate {
		t.Fatalf("credential type = %s", credential.Type)
	}
}

func TestResolveRejectsMismatchedOrDisabledMetadata(t *testing.T) {
	repository := &fakeRepository{
		target:     Target{ID: "target", DefaultCredentialID: "expected", Active: true},
		credential: Credential{ID: "other", TargetID: "target", Active: true},
	}
	service := newTestService(repository)
	if _, _, err := service.ResolveForRequest(context.Background(), "target"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ResolveForRequest() error = %v", err)
	}
}

func validHTTPInput() CreateInput {
	return CreateInput{
		Name: "tickets", Description: "ticket API", ConnectorType: ConnectorHTTP,
		ConnectionConfig: ConnectionConfig{BaseURL: "https://tickets.example.test/api", AllowedHTTPMethods: []string{"GET", "POST"}},
		CredentialAlias:  "production", CredentialType: CredentialAPIKey, VaultPath: "kv/data/tickets/production",
	}
}

func newTestService(repository Repository) *Service {
	service := NewService(repository)
	service.now = func() time.Time { return time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC) }
	ids := []string{"target-id", "credential-id"}
	service.newID = func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}
	return service
}
