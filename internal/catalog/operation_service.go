package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fallingnight/akv/internal/identity"
	secureoperation "github.com/fallingnight/akv/internal/operation"
)

var operationKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func (service *Service) ListOperationCatalog(ctx context.Context, actor identity.User) (OperationCatalog, error) {
	if !actor.CanManageUsersAndTargets() || service.operationRepository == nil {
		return OperationCatalog{}, ErrForbidden
	}
	return service.operationRepository.ListOperationCatalog(ctx)
}

func (service *Service) CreateOperationSet(ctx context.Context, actor identity.User, input CreateOperationSetInput) (OperationSet, error) {
	if !actor.CanManageUsersAndTargets() || service.operationRepository == nil {
		return OperationSet{}, ErrForbidden
	}
	input.Name, input.Description = strings.TrimSpace(input.Name), strings.TrimSpace(input.Description)
	if input.Name == "" || len(input.Name) > 128 || len(input.Description) > 2048 || !validExecutorType(input.ExecutorType) {
		return OperationSet{}, ErrInvalidInput
	}
	id, err := service.newID()
	if err != nil {
		return OperationSet{}, err
	}
	now := service.now()
	set := OperationSet{ID: id, Name: input.Name, Description: input.Description, ExecutorType: input.ExecutorType, Active: true, CreatedBy: actor.ID, UpdatedBy: actor.ID, CreatedAt: now, UpdatedAt: now}
	if err := service.operationRepository.CreateOperationSet(ctx, set); err != nil {
		return OperationSet{}, err
	}
	return set, nil
}

func (service *Service) CreateOperation(ctx context.Context, actor identity.User, operationSetID string, input PublishOperationInput) (SafeOperation, OperationVersion, error) {
	if !actor.CanManageUsersAndTargets() || service.operationRepository == nil {
		return SafeOperation{}, OperationVersion{}, ErrForbidden
	}
	input.Key = strings.TrimSpace(input.Key)
	set, ok, err := service.findOperationSet(ctx, operationSetID)
	if err != nil || !ok {
		return SafeOperation{}, OperationVersion{}, ErrUnavailable
	}
	if !operationKeyPattern.MatchString(input.Key) {
		return SafeOperation{}, OperationVersion{}, ErrInvalidInput
	}
	id, err := service.newID()
	if err != nil {
		return SafeOperation{}, OperationVersion{}, err
	}
	now := service.now()
	version, err := buildOperationVersion(id, 1, actor.ID, now, set.ExecutorType, input)
	if err != nil {
		return SafeOperation{}, OperationVersion{}, err
	}
	item := SafeOperation{ID: id, OperationSetID: set.ID, Key: input.Key, CurrentVersion: 1, Active: true, CreatedBy: actor.ID, UpdatedBy: actor.ID, CreatedAt: now, UpdatedAt: now}
	if err := service.operationRepository.CreateOperationWithVersion(ctx, item, version); err != nil {
		return SafeOperation{}, OperationVersion{}, err
	}
	return item, version, nil
}

func (service *Service) PublishOperationVersion(ctx context.Context, actor identity.User, operationID string, input PublishOperationInput) (OperationVersion, error) {
	if !actor.CanManageUsersAndTargets() || service.operationRepository == nil || operationID == "" {
		return OperationVersion{}, ErrForbidden
	}
	catalog, err := service.operationRepository.ListOperationCatalog(ctx)
	if err != nil {
		return OperationVersion{}, err
	}
	var item SafeOperation
	var set OperationSet
	found := false
	for _, candidate := range catalog.Operations {
		if candidate.ID == operationID {
			item, found = candidate, true
			break
		}
	}
	if !found {
		return OperationVersion{}, ErrUnavailable
	}
	for _, candidate := range catalog.Sets {
		if candidate.ID == item.OperationSetID {
			set = candidate
			break
		}
	}
	if set.ID == "" {
		return OperationVersion{}, ErrUnavailable
	}
	input.Key = ""
	now := service.now()
	version, err := buildOperationVersion(item.ID, item.CurrentVersion+1, actor.ID, now, set.ExecutorType, input)
	if err != nil {
		return OperationVersion{}, err
	}
	if err := service.operationRepository.PublishOperationVersion(ctx, version, item.CurrentVersion, actor.ID, now); err != nil {
		return OperationVersion{}, err
	}
	return version, nil
}

func (service *Service) SetOperationSetActive(ctx context.Context, actor identity.User, id string, active bool) error {
	if !actor.CanManageUsersAndTargets() || service.operationRepository == nil || id == "" {
		return ErrForbidden
	}
	return service.operationRepository.SetOperationSetActive(ctx, id, active, actor.ID, service.now())
}

func (service *Service) SetOperationActive(ctx context.Context, actor identity.User, id string, active bool) error {
	if !actor.CanManageUsersAndTargets() || service.operationRepository == nil || id == "" {
		return ErrForbidden
	}
	return service.operationRepository.SetOperationActive(ctx, id, active, actor.ID, service.now())
}

func (service *Service) BindOperation(ctx context.Context, actor identity.User, targetID, operationID string, version int, active bool) (TargetOperationBinding, error) {
	if !actor.CanManageUsersAndTargets() || service.operationRepository == nil || targetID == "" || operationID == "" || version <= 0 {
		return TargetOperationBinding{}, ErrForbidden
	}
	target, credential, err := service.repository.FindTargetWithDefaultCredential(ctx, targetID)
	if err != nil {
		return TargetOperationBinding{}, ErrUnavailable
	}
	set, _, definition, err := service.operationRepository.FindOperationVersion(ctx, operationID, version)
	if err != nil || !compatibleTarget(set.ExecutorType, target, credential) || !compatibleKind(set.ExecutorType, definition.Kind) || !definitionFitsTarget(definition, target) {
		return TargetOperationBinding{}, ErrInvalidInput
	}
	now := service.now()
	binding := TargetOperationBinding{TargetID: targetID, OperationID: operationID, Version: version, Active: active, Policy: json.RawMessage(`{}`), CreatedBy: actor.ID, UpdatedBy: actor.ID, CreatedAt: now, UpdatedAt: now}
	if err := service.operationRepository.UpsertTargetOperationBinding(ctx, binding); err != nil {
		return TargetOperationBinding{}, err
	}
	return binding, nil
}

func (service *Service) DiscoverOperations(ctx context.Context, authenticatedAgentID, targetID string) ([]PublicOperation, error) {
	if authenticatedAgentID == "" || targetID == "" || service.operationRepository == nil {
		return nil, ErrForbidden
	}
	if _, _, err := service.repository.FindActiveTargetAndDefaultCredential(ctx, targetID); err != nil {
		return nil, ErrUnavailable
	}
	return service.operationRepository.ListActiveTargetOperations(ctx, targetID)
}

func (service *Service) ResolveOperationForRequest(ctx context.Context, targetID, operationID string, version int) (ResolvedOperation, error) {
	if targetID == "" || operationID == "" || version <= 0 || service.operationRepository == nil {
		return ResolvedOperation{}, ErrInvalidInput
	}
	resolved, err := service.operationRepository.FindActiveOperationForRequest(ctx, targetID, operationID, version)
	if err != nil || !resolved.Target.Active || !resolved.Credential.Active || !resolved.Set.Active || !resolved.Operation.Active || resolved.Version.Version != version || resolved.Target.DefaultCredentialID != resolved.Credential.ID {
		return ResolvedOperation{}, ErrUnavailable
	}
	if !compatibleTarget(resolved.Set.ExecutorType, resolved.Target, resolved.Credential) || !compatibleKind(resolved.Set.ExecutorType, resolved.Version.Kind) {
		return ResolvedOperation{}, ErrUnavailable
	}
	return resolved, nil
}

func (service *Service) findOperationSet(ctx context.Context, id string) (OperationSet, bool, error) {
	if id == "" {
		return OperationSet{}, false, nil
	}
	catalog, err := service.operationRepository.ListOperationCatalog(ctx)
	if err != nil {
		return OperationSet{}, false, err
	}
	for _, set := range catalog.Sets {
		if set.ID == id {
			return set, true, nil
		}
	}
	return OperationSet{}, false, nil
}

func buildOperationVersion(operationID string, version int, actorID string, now time.Time, executor ExecutorType, input PublishOperationInput) (OperationVersion, error) {
	input.Name, input.Description = strings.TrimSpace(input.Name), strings.TrimSpace(input.Description)
	if input.Name == "" || len(input.Name) > 128 || len(input.Description) > 2048 || !validRiskLevel(input.RiskLevel) {
		return OperationVersion{}, ErrInvalidInput
	}
	if err := secureoperation.ValidateDefinition(input.ArgumentsSchema, input.ExecutionTemplate); err != nil {
		return OperationVersion{}, ErrInvalidInput
	}
	var template secureoperation.Template
	if err := json.Unmarshal(input.ExecutionTemplate, &template); err != nil || !compatibleKind(executor, string(template.Kind)) {
		return OperationVersion{}, ErrInvalidInput
	}
	canonicalSchema, err := canonicalJSON(input.ArgumentsSchema)
	if err != nil {
		return OperationVersion{}, ErrInvalidInput
	}
	canonicalTemplate, err := canonicalJSON(input.ExecutionTemplate)
	if err != nil {
		return OperationVersion{}, ErrInvalidInput
	}
	definition, err := json.Marshal(struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		RiskLevel   RiskLevel       `json:"risk_level"`
		Schema      json.RawMessage `json:"arguments_schema"`
		Template    json.RawMessage `json:"execution_template"`
	}{input.Name, input.Description, input.RiskLevel, canonicalSchema, canonicalTemplate})
	if err != nil {
		return OperationVersion{}, ErrInvalidInput
	}
	return OperationVersion{
		OperationID: operationID, Version: version, Name: input.Name, Description: input.Description,
		Kind: string(template.Kind), RiskLevel: input.RiskLevel,
		ArgumentsSchema: canonicalSchema, ExecutionTemplate: canonicalTemplate,
		DefinitionHash: sha256.Sum256(definition), CreatedBy: actorID, CreatedAt: now,
	}, nil
}

func canonicalJSON(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, ErrInvalidInput
	}
	return json.Marshal(value)
}

func validExecutorType(value ExecutorType) bool {
	return value == ExecutorHTTP || value == ExecutorPostgreSQL || value == ExecutorSign
}

func validRiskLevel(value RiskLevel) bool {
	return value == RiskLow || value == RiskMedium || value == RiskHigh
}

func compatibleKind(executor ExecutorType, kind string) bool {
	switch executor {
	case ExecutorHTTP:
		return kind == string(secureoperation.OperationHTTP)
	case ExecutorPostgreSQL:
		return kind == string(secureoperation.OperationPostgreSQLStatement) || kind == string(secureoperation.OperationPostgreSQLTransaction)
	case ExecutorSign:
		return kind == string(secureoperation.OperationSign)
	default:
		return false
	}
}

func compatibleTarget(executor ExecutorType, target Target, credential Credential) bool {
	switch executor {
	case ExecutorHTTP:
		return target.ConnectorType == ConnectorHTTP && (credential.Type == CredentialAPIKey || credential.Type == CredentialAccessToken || credential.Type == CredentialUsernamePassword)
	case ExecutorPostgreSQL:
		return target.ConnectorType == ConnectorPostgreSQL && (credential.Type == CredentialUsernamePassword || credential.Type == CredentialPostgreSQLDynamic)
	case ExecutorSign:
		return credential.Type == CredentialTransitKey
	default:
		return false
	}
}

func definitionFitsTarget(definition OperationVersion, target Target) bool {
	if definition.Kind != string(secureoperation.OperationHTTP) {
		return true
	}
	var template secureoperation.Template
	if json.Unmarshal(definition.ExecutionTemplate, &template) != nil || template.HTTP == nil {
		return false
	}
	return slices.Contains(target.ConnectionConfig.AllowedHTTPMethods, template.HTTP.Method)
}
