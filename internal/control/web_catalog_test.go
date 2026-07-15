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
