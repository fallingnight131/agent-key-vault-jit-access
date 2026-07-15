package operation

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRejectsUnsafeHTTPTemplates(t *testing.T) {
	noArguments := emptySchema
	pathSchema := `{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`
	optionalPathSchema := `{"type":"object","properties":{"id":{"type":"string"}},"required":[],"additionalProperties":false}`
	integerPathSchema := `{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"],"additionalProperties":false}`
	tests := []struct {
		name     string
		schema   string
		template string
	}{
		{"absolute URL", noArguments, `{"kind":"HTTP","http":{"method":"GET","path":"https://example.test/run"}}`},
		{"scheme relative URL", noArguments, `{"kind":"HTTP","http":{"method":"GET","path":"//example.test/run"}}`},
		{"query in path", noArguments, `{"kind":"HTTP","http":{"method":"GET","path":"/run?q=x"}}`},
		{"encoded path", noArguments, `{"kind":"HTTP","http":{"method":"GET","path":"/run/%2e%2e"}}`},
		{"dot segment", noArguments, `{"kind":"HTTP","http":{"method":"GET","path":"/run/../admin"}}`},
		{"empty segment", noArguments, `{"kind":"HTTP","http":{"method":"GET","path":"/run//admin"}}`},
		{"embedded placeholder", pathSchema, `{"kind":"HTTP","http":{"method":"GET","path":"/users/prefix-{id}","path_arguments":{"id":"id"}}}`},
		{"missing placeholder binding", pathSchema, `{"kind":"HTTP","http":{"method":"GET","path":"/users/{id}"}}`},
		{"unknown placeholder binding", pathSchema, `{"kind":"HTTP","http":{"method":"GET","path":"/users/{id}","path_arguments":{"other":"id"}}}`},
		{"optional path binding", optionalPathSchema, `{"kind":"HTTP","http":{"method":"GET","path":"/users/{id}","path_arguments":{"id":"id"}}}`},
		{"non-string path binding", integerPathSchema, `{"kind":"HTTP","http":{"method":"GET","path":"/users/{id}","path_arguments":{"id":"id"}}}`},
		{"undeclared query binding", noArguments, `{"kind":"HTTP","http":{"method":"GET","path":"/run","query_arguments":{"q":"missing"}}}`},
		{"unbound schema property", pathSchema, `{"kind":"HTTP","http":{"method":"GET","path":"/run"}}`},
		{"unsupported method", noArguments, `{"kind":"HTTP","http":{"method":"CONNECT","path":"/run"}}`},
		{"authorization header", noArguments, `{"kind":"HTTP","http":{"method":"GET","path":"/run","static_headers":{"authorization":"fixed"}}}`},
		{"host header", noArguments, `{"kind":"HTTP","http":{"method":"GET","path":"/run","static_headers":{"HOST":"example.test"}}}`},
		{"cookie header", noArguments, `{"kind":"HTTP","http":{"method":"GET","path":"/run","static_headers":{"Cookie":"a=b"}}}`},
		{"API key header", noArguments, `{"kind":"HTTP","http":{"method":"GET","path":"/run","static_headers":{"x-api-key":"fixed"}}}`},
		{"transfer encoding header", noArguments, `{"kind":"HTTP","http":{"method":"GET","path":"/run","static_headers":{"Transfer-Encoding":"chunked"}}}`},
		{"header newline", noArguments, `{"kind":"HTTP","http":{"method":"GET","path":"/run","static_headers":{"X-Test":"safe\r\nAuthorization: bad"}}}`},
		{"invalid header name", noArguments, `{"kind":"HTTP","http":{"method":"GET","path":"/run","static_headers":{"Bad Header":"x"}}}`},
		{"case duplicate headers", noArguments, `{"kind":"HTTP","http":{"method":"GET","path":"/run","static_headers":{"X-Test":"a","x-test":"b"}}}`},
		{"unknown template keyword", noArguments, `{"kind":"HTTP","http":{"method":"GET","path":"/run","redirects":true}}`},
		{"incorrect root keyword case", noArguments, `{"Kind":"HTTP","http":{"method":"GET","path":"/run"}}`},
		{"incorrect nested keyword case", noArguments, `{"kind":"HTTP","http":{"Method":"GET","path":"/run"}}`},
		{"case semantic duplicate", noArguments, `{"kind":"HTTP","Kind":"HTTP","http":{"method":"GET","path":"/run"}}`},
		{"mixed operation union", noArguments, `{"kind":"HTTP","http":{"method":"GET","path":"/run"},"sign":{"algorithm":"sha2-256","digest_argument":"digest"}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateDefinition([]byte(test.schema), []byte(test.template))
			if !errors.Is(err, ErrInvalidTemplate) {
				t.Fatalf("error = %v, want ErrInvalidTemplate", err)
			}
		})
	}
}

func TestRejectsUnsafePathArguments(t *testing.T) {
	schema := `{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`
	template := `{"kind":"HTTP","http":{"method":"GET","path":"/users/{id}","path_arguments":{"id":"id"}}}`
	for _, value := range []string{"", ".", "..", "a/b", `a\b`, "%2e%2e", "%252f", "a\x00b", "a\rb"} {
		t.Run(fmt.Sprintf("value_%q", value), func(t *testing.T) {
			arguments, err := jsonObject("id", value)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = Compile([]byte(schema), []byte(template), arguments)
			if !errors.Is(err, ErrInvalidArguments) {
				t.Fatalf("error = %v, want ErrInvalidArguments", err)
			}
		})
	}
}

func TestRejectsUnsafePostgreSQLTemplates(t *testing.T) {
	schema := `{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"],"additionalProperties":false}`
	optionalSchema := `{"type":"object","properties":{"id":{"type":"integer"}},"required":[],"additionalProperties":false}`
	tests := []struct {
		name     string
		schema   string
		template string
	}{
		{"empty SQL", emptySchema, `{"kind":"POSTGRESQL_STATEMENT","postgresql":{"statements":[{"sql":"  "}]}}`},
		{"NUL SQL", emptySchema, `{"kind":"POSTGRESQL_STATEMENT","postgresql":{"statements":[{"sql":"SELECT\u0000 1"}]}}`},
		{"semicolon SQL", emptySchema, `{"kind":"POSTGRESQL_STATEMENT","postgresql":{"statements":[{"sql":"SELECT 1;"}]}}`},
		{"semicolon in literal", emptySchema, `{"kind":"POSTGRESQL_STATEMENT","postgresql":{"statements":[{"sql":"SELECT ';'"}]}}`},
		{"no statements", emptySchema, `{"kind":"POSTGRESQL_STATEMENT","postgresql":{"statements":[]}}`},
		{"multiple single statements", emptySchema, `{"kind":"POSTGRESQL_STATEMENT","postgresql":{"statements":[{"sql":"SELECT 1"},{"sql":"SELECT 2"}]}}`},
		{"undeclared bind", emptySchema, `{"kind":"POSTGRESQL_STATEMENT","postgresql":{"statements":[{"sql":"SELECT $1","arguments":["id"]}]}}`},
		{"optional positional bind", optionalSchema, `{"kind":"POSTGRESQL_STATEMENT","postgresql":{"statements":[{"sql":"SELECT $1","arguments":["id"]}]}}`},
		{"unbound argument", schema, `{"kind":"POSTGRESQL_STATEMENT","postgresql":{"statements":[{"sql":"SELECT 1"}]}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateDefinition([]byte(test.schema), []byte(test.template))
			if !errors.Is(err, ErrInvalidTemplate) {
				t.Fatalf("error = %v, want ErrInvalidTemplate", err)
			}
		})
	}

	statements := make([]string, maxSQLStatements+1)
	for index := range statements {
		statements[index] = `{"sql":"SELECT 1"}`
	}
	template := `{"kind":"POSTGRESQL_TRANSACTION","postgresql":{"statements":[` + strings.Join(statements, ",") + `]}}`
	if err := ValidateDefinition([]byte(emptySchema), []byte(template)); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("too many statements error = %v, want ErrInvalidTemplate", err)
	}
}

func TestRejectsUnsafeSignDefinitionsAndArguments(t *testing.T) {
	schema := `{"type":"object","properties":{"digest":{"type":"string"}},"required":["digest"],"additionalProperties":false}`
	optionalSchema := `{"type":"object","properties":{"digest":{"type":"string"}},"required":[],"additionalProperties":false}`
	numberSchema := `{"type":"object","properties":{"digest":{"type":"integer"}},"required":["digest"],"additionalProperties":false}`
	for name, test := range map[string]struct{ schema, template string }{
		"unsupported algorithm": {schema, `{"kind":"SIGN","sign":{"algorithm":"sha1","digest_argument":"digest"}}`},
		"undeclared digest":     {emptySchema, `{"kind":"SIGN","sign":{"algorithm":"sha2-256","digest_argument":"digest"}}`},
		"optional digest":       {optionalSchema, `{"kind":"SIGN","sign":{"algorithm":"sha2-256","digest_argument":"digest"}}`},
		"non-string digest":     {numberSchema, `{"kind":"SIGN","sign":{"algorithm":"sha2-256","digest_argument":"digest"}}`},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateDefinition([]byte(test.schema), []byte(test.template)); !errors.Is(err, ErrInvalidTemplate) {
				t.Fatalf("error = %v, want ErrInvalidTemplate", err)
			}
		})
	}

	template := `{"kind":"SIGN","sign":{"algorithm":"sha2-256","digest_argument":"digest"}}`
	tests := map[string]string{
		"invalid base64":        "not base64!",
		"URL-safe base64":       base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0xff}, 32)),
		"wrong digest length":   base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 31)),
		"non-canonical padding": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)) + "=",
	}
	for name, digest := range tests {
		t.Run(name, func(t *testing.T) {
			arguments, err := jsonObject("digest", digest)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = Compile([]byte(schema), []byte(template), arguments)
			if !errors.Is(err, ErrInvalidArguments) {
				t.Fatalf("error = %v, want ErrInvalidArguments", err)
			}
		})
	}
}

func TestRejectsOversizedTemplate(t *testing.T) {
	oversized := bytes.Repeat([]byte{' '}, MaxDocumentSize+1)
	if err := ValidateDefinition([]byte(emptySchema), oversized); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("error = %v, want ErrInvalidTemplate", err)
	}
}

func TestRejectsBindingAmplification(t *testing.T) {
	schema := `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`
	bindings := make([]string, MaxBindings+1)
	for index := range bindings {
		bindings[index] = `"value"`
	}
	template := `{"kind":"POSTGRESQL_STATEMENT","postgresql":{"statements":[{"sql":"SELECT 1","arguments":[` + strings.Join(bindings, ",") + `]}]}}`
	if err := ValidateDefinition([]byte(schema), []byte(template)); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("binding count error = %v, want ErrInvalidTemplate", err)
	}

	queryBindings := make([]string, 0, 40)
	for index := 0; index < 40; index++ {
		queryBindings = append(queryBindings, fmt.Sprintf(`"q%d":"value"`, index))
	}
	httpTemplate := `{"kind":"HTTP","http":{"method":"GET","path":"/run","query_arguments":{` + strings.Join(queryBindings, ",") + `}}}`
	arguments, err := jsonObject("value", strings.Repeat("x", MaxStringBytes))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Compile([]byte(schema), []byte(httpTemplate), arguments); !errors.Is(err, ErrInvalidArguments) {
		t.Fatalf("resolved size error = %v, want ErrInvalidArguments", err)
	}
}

func jsonObject(name, value string) ([]byte, error) {
	return []byte(fmt.Sprintf(`{%q:%q}`, name, value)), nil
}
