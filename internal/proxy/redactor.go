package proxy

import (
	"bytes"
	"encoding/base64"
	"net/url"
	"sort"

	"github.com/fallingnight/akv/internal/vault"
)

var redaction = []byte("[REDACTED]")

type Redactor struct {
	patterns [][]byte
}

func NewRedactor(values map[string]*vault.SensitiveValue) *Redactor {
	redactor := &Redactor{}
	rawValues := make(map[string][]byte, len(values))
	for name, value := range values {
		_ = value.WithBytes(func(secret []byte) error {
			rawValues[name] = append([]byte(nil), secret...)
			redactor.add(secret)
			redactor.add([]byte(base64.StdEncoding.EncodeToString(secret)))
			redactor.add([]byte(base64.RawStdEncoding.EncodeToString(secret)))
			redactor.add([]byte(base64.URLEncoding.EncodeToString(secret)))
			redactor.add([]byte(url.QueryEscape(string(secret))))
			return nil
		})
	}
	if username, usernameOK := rawValues["username"]; usernameOK {
		if password, passwordOK := rawValues["password"]; passwordOK {
			basic := append(append(append([]byte(nil), username...), ':'), password...)
			redactor.add(basic)
			redactor.add([]byte(base64.StdEncoding.EncodeToString(basic)))
			for index := range basic {
				basic[index] = 0
			}
		}
	}
	for _, value := range rawValues {
		for index := range value {
			value[index] = 0
		}
	}
	sort.Slice(redactor.patterns, func(i, j int) bool { return len(redactor.patterns[i]) > len(redactor.patterns[j]) })
	return redactor
}

func (redactor *Redactor) add(pattern []byte) {
	if len(pattern) < 4 {
		return
	}
	for _, existing := range redactor.patterns {
		if bytes.Equal(existing, pattern) {
			return
		}
	}
	redactor.patterns = append(redactor.patterns, append([]byte(nil), pattern...))
}

func (redactor *Redactor) Bytes(value []byte) []byte {
	result := append([]byte(nil), value...)
	for _, pattern := range redactor.patterns {
		result = bytes.ReplaceAll(result, pattern, redaction)
	}
	return result
}

func (redactor *Redactor) String(value string) string {
	return string(redactor.Bytes([]byte(value)))
}

func (redactor *Redactor) Destroy() {
	for _, pattern := range redactor.patterns {
		for index := range pattern {
			pattern[index] = 0
		}
	}
	redactor.patterns = nil
}
