package control

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/identity"
	"github.com/fallingnight/akv/internal/vault"
)

type WebCatalogManager interface {
	ListCatalog(context.Context, identity.User) ([]catalog.Target, []catalog.Credential, error)
	ListOperationCatalog(context.Context, identity.User) (catalog.OperationCatalog, error)
	ProvisionTarget(context.Context, identity.User, catalog.ProvisionInput) (catalog.Target, catalog.Credential, error)
	UpdateTarget(context.Context, identity.User, string, string, catalog.ConnectionConfig) error
	SetTargetActive(context.Context, identity.User, string, bool) error
	SetCredentialActive(context.Context, identity.User, string, bool) error
	UpdateCredential(context.Context, identity.User, string, catalog.CredentialUpdate) error
	CreateOperationSet(context.Context, identity.User, catalog.CreateOperationSetInput) (catalog.OperationSet, error)
	CreateOperation(context.Context, identity.User, string, catalog.PublishOperationInput) (catalog.SafeOperation, catalog.OperationVersion, error)
	PublishOperationVersion(context.Context, identity.User, string, catalog.PublishOperationInput) (catalog.OperationVersion, error)
	SetOperationSetActive(context.Context, identity.User, string, bool) error
	SetOperationActive(context.Context, identity.User, string, bool) error
	BindOperation(context.Context, identity.User, string, string, int, bool) (catalog.TargetOperationBinding, error)
}

func (runtime *WebRuntime) listCatalog(response http.ResponseWriter, request *http.Request) {
	actor, _, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	targets, credentials, err := runtime.Catalog.ListCatalog(request.Context(), actor)
	if err != nil {
		writeJSON(response, http.StatusForbidden, map[string]string{"error": "FORBIDDEN"})
		return
	}
	targetResult := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		targetResult = append(targetResult, targetDTO(target))
	}
	credentialResult := make([]map[string]any, 0, len(credentials))
	for _, credential := range credentials {
		credentialResult = append(credentialResult, credentialDTOWithoutSecret(credential))
	}
	operationCatalog, err := runtime.Catalog.ListOperationCatalog(request.Context(), actor)
	if err != nil {
		writeJSON(response, http.StatusForbidden, map[string]string{"error": "FORBIDDEN"})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"targets": targetResult, "credentials": credentialResult,
		"operation_sets": operationCatalog.Sets, "operations": operationCatalog.Operations,
		"operation_versions": operationCatalog.Versions, "operation_bindings": operationCatalog.Bindings,
	})
}

type catalogWriteInput struct {
	Name             string                   `json:"name"`
	Description      string                   `json:"description"`
	ConnectorType    catalog.ConnectorType    `json:"connector_type"`
	ConnectionConfig catalog.ConnectionConfig `json:"connection_config"`
	CredentialAlias  string                   `json:"credential_alias"`
	CredentialType   catalog.CredentialType   `json:"credential_type"`
	SecretValues     map[string][]byte        `json:"secret_values,omitempty"`
	TransitKeyType   string                   `json:"transit_key_type,omitempty"`
	DatabaseRole     *vault.DatabaseRole      `json:"database_role,omitempty"`
}

func (runtime *WebRuntime) createTarget(response http.ResponseWriter, request *http.Request) {
	actor, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	var input catalogWriteInput
	if !decodeStrict(response, request, &input) {
		destroyRawSecrets(input.SecretValues)
		return
	}
	secrets := sensitiveValues(input.SecretValues)
	defer destroySensitiveValues(secrets)
	defer destroyRawSecrets(input.SecretValues)
	target, credential, err := runtime.Catalog.ProvisionTarget(request.Context(), actor, catalog.ProvisionInput{CreateInput: catalog.CreateInput{Name: input.Name, Description: input.Description, ConnectorType: input.ConnectorType, ConnectionConfig: input.ConnectionConfig, CredentialAlias: input.CredentialAlias, CredentialType: input.CredentialType}, SecretValues: secrets, TransitKeyType: input.TransitKeyType, DatabaseRole: input.DatabaseRole})
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "PROVISIONING_FAILED"})
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"target": targetDTO(target), "credential": credentialDTOWithoutSecret(credential)})
}

func (runtime *WebRuntime) updateTarget(response http.ResponseWriter, request *http.Request) {
	actor, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	var input struct {
		Description      string                   `json:"description"`
		ConnectionConfig catalog.ConnectionConfig `json:"connection_config"`
		Active           *bool                    `json:"active,omitempty"`
	}
	if !decodeStrict(response, request, &input) {
		return
	}
	if err := runtime.Catalog.UpdateTarget(request.Context(), actor, request.PathValue("target_id"), input.Description, input.ConnectionConfig); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_REQUEST"})
		return
	}
	if input.Active != nil {
		if err := runtime.Catalog.SetTargetActive(request.Context(), actor, request.PathValue("target_id"), *input.Active); err != nil {
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_REQUEST"})
			return
		}
	}
	response.WriteHeader(http.StatusNoContent)
}

func (runtime *WebRuntime) updateCredential(response http.ResponseWriter, request *http.Request) {
	actor, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	var input struct {
		SecretValues   map[string][]byte   `json:"secret_values,omitempty"`
		TransitKeyType string              `json:"transit_key_type,omitempty"`
		DatabaseRole   *vault.DatabaseRole `json:"database_role,omitempty"`
		Active         *bool               `json:"active,omitempty"`
	}
	if !decodeStrict(response, request, &input) {
		destroyRawSecrets(input.SecretValues)
		return
	}
	defer destroyRawSecrets(input.SecretValues)
	secrets := sensitiveValues(input.SecretValues)
	defer destroySensitiveValues(secrets)
	if len(secrets) == 0 && input.TransitKeyType == "" && input.DatabaseRole == nil && input.Active == nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_REQUEST"})
		return
	}
	if len(secrets) > 0 || input.TransitKeyType != "" || input.DatabaseRole != nil {
		if err := runtime.Catalog.UpdateCredential(request.Context(), actor, request.PathValue("credential_id"), catalog.CredentialUpdate{SecretValues: secrets, TransitKeyType: input.TransitKeyType, DatabaseRole: input.DatabaseRole}); err != nil {
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": "PROVISIONING_FAILED"})
			return
		}
	}
	if input.Active != nil {
		if err := runtime.Catalog.SetCredentialActive(request.Context(), actor, request.PathValue("credential_id"), *input.Active); err != nil {
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_REQUEST"})
			return
		}
	}
	response.WriteHeader(http.StatusNoContent)
}

func (runtime *WebRuntime) createOperationSet(response http.ResponseWriter, request *http.Request) {
	actor, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	var input struct {
		Name         string               `json:"name"`
		Description  string               `json:"description"`
		ExecutorType catalog.ExecutorType `json:"executor_type"`
	}
	if !decodeStrict(response, request, &input) {
		return
	}
	set, err := runtime.Catalog.CreateOperationSet(request.Context(), actor, catalog.CreateOperationSetInput{Name: input.Name, Description: input.Description, ExecutorType: input.ExecutorType})
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_OPERATION_SET"})
		return
	}
	writeJSON(response, http.StatusCreated, set)
}

type operationVersionInput struct {
	Key               string            `json:"key,omitempty"`
	Name              string            `json:"name"`
	Description       string            `json:"description"`
	RiskLevel         catalog.RiskLevel `json:"risk_level"`
	ArgumentsSchema   json.RawMessage   `json:"arguments_schema"`
	ExecutionTemplate json.RawMessage   `json:"execution_template"`
}

func (input operationVersionInput) catalogInput() catalog.PublishOperationInput {
	return catalog.PublishOperationInput{Key: input.Key, Name: input.Name, Description: input.Description, RiskLevel: input.RiskLevel, ArgumentsSchema: input.ArgumentsSchema, ExecutionTemplate: input.ExecutionTemplate}
}

func (runtime *WebRuntime) createOperation(response http.ResponseWriter, request *http.Request) {
	actor, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	var input operationVersionInput
	if !decodeStrict(response, request, &input) {
		return
	}
	item, version, err := runtime.Catalog.CreateOperation(request.Context(), actor, request.PathValue("set_id"), input.catalogInput())
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_OPERATION"})
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"operation": item, "version": version})
}

func (runtime *WebRuntime) publishOperationVersion(response http.ResponseWriter, request *http.Request) {
	actor, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	var input operationVersionInput
	if !decodeStrict(response, request, &input) {
		return
	}
	if input.Key != "" {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_OPERATION"})
		return
	}
	version, err := runtime.Catalog.PublishOperationVersion(request.Context(), actor, request.PathValue("operation_id"), input.catalogInput())
	if err != nil {
		writeJSON(response, http.StatusConflict, map[string]string{"error": "OPERATION_VERSION_CONFLICT"})
		return
	}
	writeJSON(response, http.StatusCreated, version)
}

func (runtime *WebRuntime) setOperationSetActive(response http.ResponseWriter, request *http.Request) {
	actor, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	var input struct {
		Active bool `json:"active"`
	}
	if !decodeStrict(response, request, &input) {
		return
	}
	if err := runtime.Catalog.SetOperationSetActive(request.Context(), actor, request.PathValue("set_id"), input.Active); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_REQUEST"})
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (runtime *WebRuntime) setOperationActive(response http.ResponseWriter, request *http.Request) {
	actor, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	var input struct {
		Active bool `json:"active"`
	}
	if !decodeStrict(response, request, &input) {
		return
	}
	if err := runtime.Catalog.SetOperationActive(request.Context(), actor, request.PathValue("operation_id"), input.Active); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_REQUEST"})
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (runtime *WebRuntime) bindOperation(response http.ResponseWriter, request *http.Request) {
	actor, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	var input struct {
		Version int  `json:"version"`
		Active  bool `json:"active"`
	}
	if !decodeStrict(response, request, &input) {
		return
	}
	binding, err := runtime.Catalog.BindOperation(request.Context(), actor, request.PathValue("target_id"), request.PathValue("operation_id"), input.Version, input.Active)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_OPERATION_BINDING"})
		return
	}
	writeJSON(response, http.StatusOK, binding)
}

func sensitiveValues(raw map[string][]byte) map[string]*vault.SensitiveValue {
	values := make(map[string]*vault.SensitiveValue, len(raw))
	for name, value := range raw {
		values[name] = vault.NewSensitiveValue(value)
	}
	return values
}
func destroyRawSecrets(values map[string][]byte) {
	for _, value := range values {
		for index := range value {
			value[index] = 0
		}
	}
}
func destroySensitiveValues(values map[string]*vault.SensitiveValue) {
	for _, value := range values {
		value.Destroy()
	}
}
func targetDTO(target catalog.Target) map[string]any {
	return map[string]any{"id": target.ID, "name": target.Name, "description": target.Description, "connector_type": target.ConnectorType, "connection_config": target.ConnectionConfig, "config_version": target.ConfigVersion, "active": target.Active, "default_credential_id": target.DefaultCredentialID}
}
func credentialDTOWithoutSecret(credential catalog.Credential) map[string]any {
	return map[string]any{"id": credential.ID, "target_id": credential.TargetID, "alias": credential.Alias, "credential_type": credential.Type, "active": credential.Active}
}
