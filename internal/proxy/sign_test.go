package proxy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/vault"
)

type signVault struct{ calls int }

func (*signVault) ReadKV(context.Context, string, *int) (map[string]*vault.SensitiveValue, error) {
	return nil, errors.New("unexpected read")
}
func (client *signVault) Sign(_ context.Context, path, algorithm string, digest []byte) ([]byte, error) {
	client.calls++
	if path != "transit/keys/signing" || algorithm != "sha2-256" || string(digest) != "fixture-digest" {
		return nil, errors.New("unexpected sign input")
	}
	return []byte("vault-signature"), nil
}
func (*signVault) IssueDatabase(context.Context, string, time.Duration) (vault.DynamicCredential, error) {
	return vault.DynamicCredential{}, errors.New("unexpected issue")
}
func (*signVault) RevokeLease(context.Context, string) error { return nil }

func TestSignClaimsBeforeTransitAndReturnsOnlySignature(t *testing.T) {
	plan := validPlan("https://unused.example.test")
	plan.Credential.Type, plan.Credential.VaultPath = catalog.CredentialTransitKey, "transit/keys/signing"
	plan.Operation = authorization.Operation{
		Kind: authorization.OperationSign,
		Sign: &authorization.SignParameters{DigestAlgorithm: "sha2-256", Digest: []byte("fixture-digest")},
	}
	vaultClient := &signVault{}
	guard := &fakeGuard{}
	proxy := NewSignProxy(&fakePlans{plan}, guard, vaultClient, &fakeLifecycle{})
	signature, err := proxy.Execute(context.Background(), "request", "agent", "task")
	if err != nil || string(signature) != "vault-signature" || guard.calls != 1 || vaultClient.calls != 1 {
		t.Fatalf("signature=%q error=%v guard=%d transit=%d", signature, err, guard.calls, vaultClient.calls)
	}
}

func TestSignDeniedDoesNotCallTransit(t *testing.T) {
	plan := validPlan("https://unused.example.test")
	plan.Credential.Type = catalog.CredentialTransitKey
	plan.Operation = authorization.Operation{Kind: authorization.OperationSign, Sign: &authorization.SignParameters{Digest: []byte("fixture-digest")}}
	vaultClient := &signVault{}
	proxy := NewSignProxy(&fakePlans{plan}, &fakeGuard{err: authorization.ErrClaimDenied}, vaultClient, &fakeLifecycle{})
	if _, err := proxy.Execute(context.Background(), "request", "agent", "task"); !errors.Is(err, ErrExecutionDenied) {
		t.Fatalf("Execute() error = %v", err)
	}
	if vaultClient.calls != 0 {
		t.Fatalf("Transit calls = %d", vaultClient.calls)
	}
}
