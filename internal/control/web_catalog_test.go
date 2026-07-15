package control

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/identity"
)

type webCatalogFake struct {
	secret     string
	target     catalog.Target
	credential catalog.Credential
}

func (fake *webCatalogFake) ListCatalog(context.Context, identity.User) ([]catalog.Target, []catalog.Credential, error) {
	return []catalog.Target{fake.target}, []catalog.Credential{fake.credential}, nil
}
func (*webCatalogFake) ListOperationCatalog(context.Context, identity.User) (catalog.OperationCatalog, error) {
	return catalog.OperationCatalog{}, nil
}
func (fake *webCatalogFake) ProvisionTarget(_ context.Context, _ identity.User, input catalog.ProvisionInput) (catalog.Target, catalog.Credential, error) {
	_ = input.SecretValues["api_key"].WithBytes(func(value []byte) error { fake.secret = string(value); return nil })
	fake.target = catalog.Target{ID: "target", Name: input.Name, ConnectorType: input.ConnectorType, ConnectionConfig: input.ConnectionConfig, DefaultCredentialID: "credential", Active: true}
	fake.credential = catalog.Credential{ID: "credential", TargetID: "target", Alias: input.CredentialAlias, Type: input.CredentialType, VaultPath: "kv/data/credentials/credential", Active: true}
	return fake.target, fake.credential, nil
}
func (*webCatalogFake) UpdateTarget(context.Context, identity.User, string, string, catalog.ConnectionConfig) error {
	return nil
}
func (*webCatalogFake) SetTargetActive(context.Context, identity.User, string, bool) error {
	return nil
}
func (*webCatalogFake) SetCredentialActive(context.Context, identity.User, string, bool) error {
	return nil
}
func (*webCatalogFake) UpdateCredential(context.Context, identity.User, string, catalog.CredentialUpdate) error {
	return nil
}
func (*webCatalogFake) CreateOperationSet(_ context.Context, actor identity.User, input catalog.CreateOperationSetInput) (catalog.OperationSet, error) {
	return catalog.OperationSet{ID: "set", Name: input.Name, Description: input.Description, ExecutorType: input.ExecutorType, Active: true, CreatedBy: actor.ID}, nil
}
func (*webCatalogFake) CreateOperation(_ context.Context, _ identity.User, setID string, input catalog.PublishOperationInput) (catalog.SafeOperation, catalog.OperationVersion, error) {
	return catalog.SafeOperation{ID: "operation", OperationSetID: setID, Key: input.Key, CurrentVersion: 1, Active: true}, catalog.OperationVersion{OperationID: "operation", Version: 1, Name: input.Name, RiskLevel: input.RiskLevel, ArgumentsSchema: input.ArgumentsSchema, ExecutionTemplate: input.ExecutionTemplate}, nil
}
func (*webCatalogFake) PublishOperationVersion(_ context.Context, _ identity.User, operationID string, input catalog.PublishOperationInput) (catalog.OperationVersion, error) {
	return catalog.OperationVersion{OperationID: operationID, Version: 2, Name: input.Name, RiskLevel: input.RiskLevel}, nil
}
func (*webCatalogFake) SetOperationSetActive(context.Context, identity.User, string, bool) error {
	return nil
}
func (*webCatalogFake) SetOperationActive(context.Context, identity.User, string, bool) error {
	return nil
}
func (*webCatalogFake) BindOperation(_ context.Context, actor identity.User, targetID, operationID string, version int, active bool) (catalog.TargetOperationBinding, error) {
	return catalog.TargetOperationBinding{TargetID: targetID, OperationID: operationID, Version: version, Active: active, CreatedBy: actor.ID}, nil
}

func TestWebCatalogProvisioningDoesNotReturnSecretOrVaultReference(t *testing.T) {
	fake := &webCatalogFake{}
	config := Config{ListenAddress: "127.0.0.1:0", PublicOrigin: "https://akv.example.test"}
	server := NewServer(config, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), nil, &WebRuntime{Identity: &webIdentityFake{}, Catalog: fake})
	body := `{"name":"tickets","description":"fixture","connector_type":"HTTP","connection_config":{"base_url":"https://target.example.test","allowed_http_methods":["POST"]},"credential_alias":"default","credential_type":"API_KEY","secret_values":{"api_key":"Zml4dHVyZS1zZWNyZXQ="}}`
	request := authenticatedWebRequest(http.MethodPost, "/v1/web/targets", body)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || fake.secret != "fixture-secret" || strings.Contains(response.Body.String(), "fixture-secret") || strings.Contains(response.Body.String(), "vault") || strings.Contains(response.Body.String(), "kv/data") {
		t.Fatalf("status=%d secret=%q body=%q", response.Code, fake.secret, response.Body.String())
	}
	request = authenticatedWebRequest(http.MethodGet, "/v1/web/catalog", "")
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "kv/data") || strings.Contains(response.Body.String(), "vault") {
		t.Fatalf("list status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestWebAdministratorPublishesAndBindsSafeOperationVersion(t *testing.T) {
	fake := &webCatalogFake{}
	config := Config{ListenAddress: "127.0.0.1:0", PublicOrigin: "https://akv.example.test"}
	server := NewServer(config, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), nil, &WebRuntime{Identity: &webIdentityFake{}, Catalog: fake})

	request := authenticatedWebRequest(http.MethodPost, "/v1/web/operation-sets", `{"name":"ticket database","description":"reusable","executor_type":"POSTGRESQL"}`)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"id":"set"`) {
		t.Fatalf("create set status=%d body=%q", response.Code, response.Body.String())
	}

	body := `{"key":"query_ticket","name":"Query ticket","description":"safe lookup","risk_level":"LOW","arguments_schema":{"type":"object","properties":{"ticket_id":{"type":"integer"}},"required":["ticket_id"],"additionalProperties":false},"execution_template":{"kind":"POSTGRESQL_STATEMENT","postgresql":{"statements":[{"sql":"SELECT id FROM tickets WHERE id=$1","arguments":["ticket_id"]}]}}}`
	request = authenticatedWebRequest(http.MethodPost, "/v1/web/operation-sets/set/operations", body)
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"operation_set_id":"set"`) {
		t.Fatalf("create operation status=%d body=%q", response.Code, response.Body.String())
	}

	request = authenticatedWebRequest(http.MethodPut, "/v1/web/targets/target/operations/operation", `{"version":1,"active":true}`)
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"version":1`) {
		t.Fatalf("bind operation status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestWebOperationDefinitionRejectsUnknownSecretField(t *testing.T) {
	fake := &webCatalogFake{}
	config := Config{ListenAddress: "127.0.0.1:0", PublicOrigin: "https://akv.example.test"}
	server := NewServer(config, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), nil, &WebRuntime{Identity: &webIdentityFake{}, Catalog: fake})
	body := `{"key":"query","name":"Query","description":"","risk_level":"LOW","arguments_schema":{},"execution_template":{},"credential_id":"attacker"}`
	request := authenticatedWebRequest(http.MethodPost, "/v1/web/operation-sets/set/operations", body)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}
