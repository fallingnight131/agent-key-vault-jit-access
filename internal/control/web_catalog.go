package control

import (
	"context"
	"net/http"

	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/identity"
	"github.com/fallingnight/akv/internal/vault"
)

type WebCatalogManager interface {
	ListCatalog(context.Context, identity.User) ([]catalog.Target, []catalog.Credential, error)
	ProvisionTarget(context.Context, identity.User, catalog.ProvisionInput) (catalog.Target, catalog.Credential, error)
	UpdateTarget(context.Context, identity.User, string, string, catalog.ConnectionConfig) error
	SetTargetActive(context.Context, identity.User, string, bool) error
	SetCredentialActive(context.Context, identity.User, string, bool) error
	UpdateCredential(context.Context, identity.User, string, catalog.CredentialUpdate) error
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
	writeJSON(response, http.StatusOK, map[string]any{"targets": targetResult, "credentials": credentialResult})
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
	return map[string]any{"id": target.ID, "name": target.Name, "description": target.Description, "connector_type": target.ConnectorType, "connection_config": target.ConnectionConfig, "active": target.Active, "default_credential_id": target.DefaultCredentialID}
}
func credentialDTOWithoutSecret(credential catalog.Credential) map[string]any {
	return map[string]any{"id": credential.ID, "target_id": credential.TargetID, "alias": credential.Alias, "credential_type": credential.Type, "active": credential.Active}
}
