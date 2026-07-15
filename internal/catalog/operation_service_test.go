package catalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/identity"
)

type fakeOperationRepository struct {
	fakeRepository
	catalog  OperationCatalog
	resolved ResolvedOperation
}

func (repository *fakeOperationRepository) ListOperationCatalog(context.Context) (OperationCatalog, error) {
	return repository.catalog, nil
}
func (repository *fakeOperationRepository) CreateOperationSet(_ context.Context, set OperationSet) error {
	repository.catalog.Sets = append(repository.catalog.Sets, set)
	return nil
}
func (repository *fakeOperationRepository) CreateOperationWithVersion(_ context.Context, item SafeOperation, version OperationVersion) error {
	repository.catalog.Operations = append(repository.catalog.Operations, item)
	repository.catalog.Versions = append(repository.catalog.Versions, version)
	return nil
}
func (repository *fakeOperationRepository) PublishOperationVersion(_ context.Context, version OperationVersion, expectedCurrent int, actor string, at time.Time) error {
	for index := range repository.catalog.Operations {
		item := &repository.catalog.Operations[index]
		if item.ID == version.OperationID && item.CurrentVersion == expectedCurrent {
			item.CurrentVersion, item.UpdatedBy, item.UpdatedAt = version.Version, actor, at
			repository.catalog.Versions = append(repository.catalog.Versions, version)
			return nil
		}
	}
	return ErrUnavailable
}
func (repository *fakeOperationRepository) SetOperationSetActive(_ context.Context, id string, active bool, _ string, _ time.Time) error {
	for index := range repository.catalog.Sets {
		if repository.catalog.Sets[index].ID == id {
			repository.catalog.Sets[index].Active = active
			return nil
		}
	}
	return ErrUnavailable
}
func (repository *fakeOperationRepository) SetOperationActive(_ context.Context, id string, active bool, _ string, _ time.Time) error {
	for index := range repository.catalog.Operations {
		if repository.catalog.Operations[index].ID == id {
			repository.catalog.Operations[index].Active = active
			return nil
		}
	}
	return ErrUnavailable
}
func (repository *fakeOperationRepository) UpsertTargetOperationBinding(_ context.Context, binding TargetOperationBinding) error {
	repository.catalog.Bindings = []TargetOperationBinding{binding}
	return nil
}
func (repository *fakeOperationRepository) FindOperationVersion(_ context.Context, operationID string, version int) (OperationSet, SafeOperation, OperationVersion, error) {
	var set OperationSet
	var item SafeOperation
	var definition OperationVersion
	for _, candidate := range repository.catalog.Operations {
		if candidate.ID == operationID {
			item = candidate
		}
	}
	for _, candidate := range repository.catalog.Sets {
		if candidate.ID == item.OperationSetID {
			set = candidate
		}
	}
	for _, candidate := range repository.catalog.Versions {
		if candidate.OperationID == operationID && candidate.Version == version {
			definition = candidate
		}
	}
	if set.ID == "" || item.ID == "" || definition.OperationID == "" {
		return OperationSet{}, SafeOperation{}, OperationVersion{}, ErrUnavailable
	}
	return set, item, definition, nil
}
func (repository *fakeOperationRepository) ListActiveTargetOperations(_ context.Context, targetID string) ([]PublicOperation, error) {
	for _, binding := range repository.catalog.Bindings {
		if binding.TargetID == targetID && binding.Active {
			set, item, version, err := repository.FindOperationVersion(context.Background(), binding.OperationID, binding.Version)
			if err == nil && set.Active && item.Active {
				return []PublicOperation{{OperationID: item.ID, Version: version.Version, TargetID: targetID, Key: item.Key, Name: version.Name, Description: version.Description, Kind: version.Kind, RiskLevel: version.RiskLevel, ArgumentsSchema: version.ArgumentsSchema}}, nil
			}
		}
	}
	return nil, nil
}
func (repository *fakeOperationRepository) FindActiveOperationForRequest(_ context.Context, targetID, operationID string, version int) (ResolvedOperation, error) {
	for _, binding := range repository.catalog.Bindings {
		if binding.TargetID == targetID && binding.OperationID == operationID && binding.Version == version && binding.Active {
			set, item, definition, err := repository.FindOperationVersion(context.Background(), operationID, version)
			if err != nil {
				return ResolvedOperation{}, err
			}
			return ResolvedOperation{Target: repository.target, Credential: repository.credential, Set: set, Operation: item, Version: definition}, nil
		}
	}
	return ResolvedOperation{}, ErrUnavailable
}

func TestAdministratorPublishesReusableOperationAndBindsTarget(t *testing.T) {
	repository := &fakeOperationRepository{fakeRepository: fakeRepository{
		target:     Target{ID: "target", ConnectorType: ConnectorPostgreSQL, ConfigVersion: 1, DefaultCredentialID: "credential", Active: true},
		credential: Credential{ID: "credential", TargetID: "target", Type: CredentialPostgreSQLDynamic, Active: true},
	}}
	service := NewService(repository)
	ids := []string{"set", "operation"}
	service.newID = func() (string, error) { id := ids[0]; ids = ids[1:]; return id, nil }
	service.now = func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) }
	admin := identity.User{ID: "admin", IsAdmin: true, OwnerActive: true}

	set, err := service.CreateOperationSet(context.Background(), admin, CreateOperationSetInput{Name: "ticket database", ExecutorType: ExecutorPostgreSQL})
	if err != nil {
		t.Fatalf("CreateOperationSet() error = %v", err)
	}
	item, version, err := service.CreateOperation(context.Background(), admin, set.ID, validQueryOperation())
	if err != nil {
		t.Fatalf("CreateOperation() error = %v", err)
	}
	if _, err := service.BindOperation(context.Background(), admin, "target", item.ID, version.Version, true); err != nil {
		t.Fatalf("BindOperation() error = %v", err)
	}
	public, err := service.DiscoverOperations(context.Background(), "agent", "target")
	if err != nil || len(public) != 1 || public[0].OperationID != item.ID || len(public[0].ArgumentsSchema) == 0 {
		t.Fatalf("DiscoverOperations() public=%+v error=%v", public, err)
	}
	resolved, err := service.ResolveOperationForRequest(context.Background(), "target", item.ID, 1)
	if err != nil || resolved.Credential.ID != "credential" || resolved.Version.ExecutionTemplate == nil {
		t.Fatalf("ResolveOperationForRequest() resolved=%+v error=%v", resolved, err)
	}
}

func TestOperationCatalogRejectsNonAdminAndUnsafeDefinitions(t *testing.T) {
	repository := &fakeOperationRepository{}
	service := NewService(repository)
	ordinary := identity.User{ID: "user", OwnerActive: true}
	if _, err := service.CreateOperationSet(context.Background(), ordinary, CreateOperationSetInput{Name: "set", ExecutorType: ExecutorHTTP}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ordinary CreateOperationSet() error = %v", err)
	}
	repository.catalog.Sets = []OperationSet{{ID: "set", ExecutorType: ExecutorPostgreSQL, Active: true}}
	service.newID = func() (string, error) { return "operation", nil }
	unsafe := validQueryOperation()
	unsafe.ExecutionTemplate = []byte(`{"kind":"POSTGRESQL_STATEMENT","postgresql":{"statements":[{"sql":"SELECT 1; DROP TABLE users","arguments":[]}]}}`)
	if _, _, err := service.CreateOperation(context.Background(), identity.User{ID: "admin", IsAdmin: true, OwnerActive: true}, "set", unsafe); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsafe CreateOperation() error = %v", err)
	}
}

func TestPublishingCreatesNewImmutableVersionWithoutMovingBindings(t *testing.T) {
	repository := &fakeOperationRepository{}
	service := NewService(repository)
	service.newID = func() (string, error) { return "unused", nil }
	admin := identity.User{ID: "admin", IsAdmin: true, OwnerActive: true}
	repository.catalog.Sets = []OperationSet{{ID: "set", ExecutorType: ExecutorPostgreSQL, Active: true}}
	repository.catalog.Operations = []SafeOperation{{ID: "operation", OperationSetID: "set", Key: "query_ticket", CurrentVersion: 1, Active: true}}
	first, err := buildOperationVersion("operation", 1, admin.ID, time.Now(), ExecutorPostgreSQL, validQueryOperation())
	if err != nil {
		t.Fatal(err)
	}
	repository.catalog.Versions = []OperationVersion{first}
	repository.catalog.Bindings = []TargetOperationBinding{{TargetID: "target", OperationID: "operation", Version: 1, Active: true}}

	second, err := service.PublishOperationVersion(context.Background(), admin, "operation", validQueryOperation())
	if err != nil || second.Version != 2 {
		t.Fatalf("PublishOperationVersion() version=%+v error=%v", second, err)
	}
	if repository.catalog.Bindings[0].Version != 1 || repository.catalog.Versions[0].Version != 1 {
		t.Fatalf("publishing mutated old state: %+v", repository.catalog)
	}
}

func validQueryOperation() PublishOperationInput {
	return PublishOperationInput{
		Key: "query_ticket", Name: "Query ticket", RiskLevel: RiskLow,
		ArgumentsSchema:   []byte(`{"type":"object","properties":{"ticket_id":{"type":"integer","minimum":1}},"required":["ticket_id"],"additionalProperties":false}`),
		ExecutionTemplate: []byte(`{"kind":"POSTGRESQL_STATEMENT","postgresql":{"statements":[{"sql":"SELECT id FROM tickets WHERE id=$1","arguments":["ticket_id"]}]}}`),
	}
}
