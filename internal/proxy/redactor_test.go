package proxy

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/fallingnight/akv/internal/vault"
)

func TestRedactorCoversRawEncodedAndBasicCredentials(t *testing.T) {
	const username = "fixture-user"
	const password = "fixture-password"
	redactor := NewRedactor(map[string]*vault.SensitiveValue{
		"username": vault.NewSensitiveValue([]byte(username)),
		"password": vault.NewSensitiveValue([]byte(password)),
	})
	defer redactor.Destroy()
	basic := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	input := strings.Join([]string{
		username, password,
		base64.StdEncoding.EncodeToString([]byte(password)),
		basic,
	}, " | ")
	output := redactor.String(input)
	for _, secretForm := range []string{username, password, basic} {
		if strings.Contains(output, secretForm) {
			t.Fatalf("redacted output contains %q: %q", secretForm, output)
		}
	}
}
