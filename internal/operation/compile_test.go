package operation

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const emptySchema = `{
  "type":"object",
  "properties":{},
  "required":[],
  "additionalProperties":false
}`

const staticHTTPTemplate = `{
  "kind":"HTTP",
  "http":{"method":"POST","path":"/run"}
}`

func TestCompileHTTP(t *testing.T) {
	schema := `{
  "type":"object",
  "properties":{
    "id":{"type":"string","description":"user ID","maxLength":20},
    "page":{"type":"integer","minimum":1,"maximum":100},
    "score":{"type":"number","minimum":0,"maximum":10},
    "active":{"type":"boolean"},
    "note":{"type":"string","enum":["short","long"]}
  },
  "required":["id","page","score","active"],
  "additionalProperties":false
}`
	template := `{
  "kind":"HTTP",
  "http":{
    "method":"post",
    "path":"/v1/users/{user}",
    "path_arguments":{"user":"id"},
    "query_arguments":{"page":"page"},
    "static_headers":{"content-type":"application/json","X-Mode":"safe"},
    "body_arguments":["score","active","note"]
  }
}`
	resolved, canonical, err := Compile([]byte(schema), []byte(template), []byte(`{
  "score":1.5,"id":"a b?","active":true,"page":2,"note":"short"
}`))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if got, want := string(canonical), `{"active":true,"id":"a b?","note":"short","page":2,"score":1.5}`; got != want {
		t.Fatalf("canonical arguments = %s, want %s", got, want)
	}
	if resolved.Kind != OperationHTTP || resolved.HTTP == nil || resolved.PostgreSQL != nil || resolved.Sign != nil {
		t.Fatalf("resolved operation has the wrong shape: %#v", resolved)
	}
	if got, want := resolved.HTTP.Method, "POST"; got != want {
		t.Errorf("method = %q, want %q", got, want)
	}
	if got, want := resolved.HTTP.Path, "/v1/users/a%20b%3F"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if got, want := resolved.HTTP.Query["page"], []string{"2"}; !equalStrings(got, want) {
		t.Errorf("query = %#v, want %#v", got, want)
	}
	if got, want := resolved.HTTP.Headers["Content-Type"], "application/json"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got, want := string(resolved.HTTP.Body), `{"active":true,"note":"short","score":1.5}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestCompileHTTPOmitsOptionalBindings(t *testing.T) {
	schema := `{"type":"object","properties":{"q":{"type":"string"}},"required":[],"additionalProperties":false}`
	template := `{"kind":"HTTP","http":{"method":"GET","path":"/search","query_arguments":{"q":"q"}}}`
	resolved, canonical, err := Compile([]byte(schema), []byte(template), []byte(`{}`))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if got, want := string(canonical), `{}`; got != want {
		t.Fatalf("canonical = %s, want %s", got, want)
	}
	if resolved.HTTP.Query != nil || resolved.HTTP.Body != nil || resolved.HTTP.Headers != nil {
		t.Fatalf("optional values were not omitted: %#v", resolved.HTTP)
	}
}

func TestCompilePostgreSQL(t *testing.T) {
	schema := `{
  "type":"object",
  "properties":{"name":{"type":"string"},"limit":{"type":"integer"},"enabled":{"type":"boolean"}},
  "required":["name","limit","enabled"],
  "additionalProperties":false
}`
	template := `{
  "kind":"POSTGRESQL_TRANSACTION",
  "postgresql":{"statements":[
    {"sql":"UPDATE jobs SET enabled = $1 WHERE name = $2","arguments":["enabled","name"]},
    {"sql":"DELETE FROM logs WHERE name = $1 AND sequence > $2","arguments":["name","limit"]}
  ]}
}`
	resolved, canonical, err := Compile([]byte(schema), []byte(template), []byte(`{"limit":9,"enabled":false,"name":"nightly"}`))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if got, want := string(canonical), `{"enabled":false,"limit":9,"name":"nightly"}`; got != want {
		t.Fatalf("canonical = %s, want %s", got, want)
	}
	if resolved.Kind != OperationPostgreSQLTransaction || resolved.PostgreSQL == nil || len(resolved.PostgreSQL.Statements) != 2 {
		t.Fatalf("resolved operation has the wrong shape: %#v", resolved)
	}
	first := resolved.PostgreSQL.Statements[0]
	if got, want := first.Arguments, []any{false, "nightly"}; !equalValues(got, want) {
		t.Errorf("first arguments = %#v, want %#v", got, want)
	}
	second := resolved.PostgreSQL.Statements[1]
	if got, ok := second.Arguments[1].(int64); !ok || got != 9 {
		t.Errorf("integer bind = %#v (%T), want int64(9)", second.Arguments[1], second.Arguments[1])
	}
}

func TestPostgreSQLResolvedOperationRoundTripPreservesInteger(t *testing.T) {
	schema := `{
  "type":"object",
  "properties":{"count":{"type":"integer"},"ratio":{"type":"number"}},
  "required":["count","ratio"],
  "additionalProperties":false
}`
	template := `{
  "kind":"POSTGRESQL_STATEMENT",
  "postgresql":{"statements":[{"sql":"SELECT $1, $2","arguments":["count","ratio"]}]}
}`
	resolved, _, err := Compile([]byte(schema), []byte(template), []byte(`{"ratio":10,"count":9}`))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	snapshot, err := json.Marshal(resolved)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded ResolvedOperation
	if err := json.Unmarshal(snapshot, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	arguments := decoded.PostgreSQL.Statements[0].Arguments
	if value, ok := arguments[0].(int64); !ok || value != 9 {
		t.Fatalf("integer argument = %#v (%T), want int64(9)", arguments[0], arguments[0])
	}
	if value, ok := arguments[1].(float64); !ok || value != 10 {
		t.Fatalf("number argument = %#v (%T), want float64(10)", arguments[1], arguments[1])
	}
}

func TestSQLStatementUnmarshalRejectsUnsafeJSON(t *testing.T) {
	tests := map[string]string{
		"unknown field":           `{"sql":"SELECT 1","unknown":true}`,
		"incorrect field case":    `{"SQL":"SELECT 1"}`,
		"case semantic duplicate": `{"sql":"SELECT 1","SQL":"SELECT 2"}`,
		"duplicate field":         `{"sql":"SELECT 1","sql":"SELECT 2"}`,
		"missing SQL":             `{"arguments":[]}`,
		"null SQL":                `{"sql":null}`,
		"null arguments":          `{"sql":"SELECT 1","arguments":null}`,
		"non-finite bind":         `{"sql":"SELECT $1","arguments":[1e999]}`,
		"null bind":               `{"sql":"SELECT $1","arguments":[null]}`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			var statement SQLStatement
			if err := json.Unmarshal([]byte(raw), &statement); err == nil {
				t.Fatal("Unmarshal() error = nil")
			}
		})
	}
}

func TestCompileSign(t *testing.T) {
	schema := `{"type":"object","properties":{"digest":{"type":"string"}},"required":["digest"],"additionalProperties":false}`
	template := `{"kind":"SIGN","sign":{"algorithm":"sha2-384","digest_argument":"digest"}}`
	digest := bytes.Repeat([]byte{0x5a}, 48)
	arguments := []byte(fmt.Sprintf(`{"digest":%q}`, base64.StdEncoding.EncodeToString(digest)))
	resolved, _, err := Compile([]byte(schema), []byte(template), arguments)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if resolved.Kind != OperationSign || resolved.Sign == nil || resolved.Sign.DigestAlgorithm != "sha2-384" || !bytes.Equal(resolved.Sign.Digest, digest) {
		t.Fatalf("resolved sign operation = %#v", resolved)
	}
}

func TestCompileCanonicalizesNumbers(t *testing.T) {
	schema := `{
  "type":"object",
  "properties":{"integer":{"type":"integer"},"number":{"type":"number"},"zero":{"type":"number"}},
  "required":["integer","number","zero"],
  "additionalProperties":false
}`
	template := `{
  "kind":"POSTGRESQL_STATEMENT",
  "postgresql":{"statements":[{"sql":"SELECT $1, $2, $3","arguments":["integer","number","zero"]}]}
}`
	_, canonical, err := Compile([]byte(schema), []byte(template), []byte(`{"zero":-0,"number":1e1,"integer":4}`))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if got, want := string(canonical), `{"integer":4,"number":10,"zero":0}`; got != want {
		t.Fatalf("canonical = %s, want %s", got, want)
	}
}

func TestInvalidSchemas(t *testing.T) {
	properties := make(map[string]any, MaxProperties+1)
	for index := 0; index <= MaxProperties; index++ {
		properties[fmt.Sprintf("p%d", index)] = map[string]any{"type": "string"}
	}
	tooMany, err := json.Marshal(map[string]any{
		"type": "object", "properties": properties, "required": []string{}, "additionalProperties": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"unknown root keyword":            []byte(`{"type":"object","properties":{},"required":[],"additionalProperties":false,"$schema":"x"}`),
		"duplicate root key":              []byte(`{"type":"object","type":"object","properties":{},"required":[],"additionalProperties":false}`),
		"escaped duplicate key":           []byte(`{"type":"object","properties":{"a":{"type":"string"},"\u0061":{"type":"string"}},"required":[],"additionalProperties":false}`),
		"unknown property keyword":        []byte(`{"type":"object","properties":{"a":{"type":"string","pattern":".*"}},"required":[],"additionalProperties":false}`),
		"incorrect root keyword case":     []byte(`{"Type":"object","properties":{},"required":[],"additionalProperties":false}`),
		"incorrect property keyword case": []byte(`{"type":"object","properties":{"a":{"Type":"string"}},"required":[],"additionalProperties":false}`),
		"case semantic duplicate":         []byte(`{"type":"object","Type":"object","properties":{},"required":[],"additionalProperties":false}`),
		"missing additionalProperties":    []byte(`{"type":"object","properties":{},"required":[]}`),
		"allows additional properties":    []byte(`{"type":"object","properties":{},"required":[],"additionalProperties":true}`),
		"null keyword":                    []byte(`{"type":"object","properties":{},"required":null,"additionalProperties":false}`),
		"unsupported type":                []byte(`{"type":"object","properties":{"a":{"type":"array"}},"required":[],"additionalProperties":false}`),
		"unknown required property":       []byte(`{"type":"object","properties":{},"required":["a"],"additionalProperties":false}`),
		"duplicate required property":     []byte(`{"type":"object","properties":{"a":{"type":"string"}},"required":["a","a"],"additionalProperties":false}`),
		"empty enum":                      []byte(`{"type":"object","properties":{"a":{"type":"string","enum":[]}},"required":[],"additionalProperties":false}`),
		"duplicate enum":                  []byte(`{"type":"object","properties":{"a":{"type":"string","enum":["x","x"]}},"required":[],"additionalProperties":false}`),
		"numeric string":                  []byte(`{"type":"object","properties":{"a":{"type":"string","minimum":1}},"required":[],"additionalProperties":false}`),
		"string number":                   []byte(`{"type":"object","properties":{"a":{"type":"number","maxLength":4}},"required":[],"additionalProperties":false}`),
		"integer decimal bound":           []byte(`{"type":"object","properties":{"a":{"type":"integer","minimum":1.0}},"required":[],"additionalProperties":false}`),
		"non-finite bound":                []byte(`{"type":"object","properties":{"a":{"type":"number","maximum":1e999}},"required":[],"additionalProperties":false}`),
		"lossy number bound":              []byte(`{"type":"object","properties":{"a":{"type":"number","maximum":1.0000000000000001}},"required":[],"additionalProperties":false}`),
		"reversed bounds":                 []byte(`{"type":"object","properties":{"a":{"type":"integer","minimum":2,"maximum":1}},"required":[],"additionalProperties":false}`),
		"too many properties":             tooMany,
		"too long string":                 []byte(fmt.Sprintf(`{"type":"object","properties":{"a":{"type":"string","description":%q}},"required":[],"additionalProperties":false}`, strings.Repeat("x", MaxStringBytes+1))),
		"too large document":              bytes.Repeat([]byte{' '}, MaxDocumentSize+1),
	}
	for name, schema := range tests {
		t.Run(name, func(t *testing.T) {
			err := ValidateDefinition(schema, []byte(staticHTTPTemplate))
			if !errors.Is(err, ErrInvalidSchema) {
				t.Fatalf("error = %v, want ErrInvalidSchema", err)
			}
		})
	}
}

func TestInvalidArguments(t *testing.T) {
	schema := `{
  "type":"object",
  "properties":{
    "name":{"type":"string","enum":["alpha","beta"],"maxLength":5},
    "count":{"type":"integer","minimum":1,"maximum":3},
    "ratio":{"type":"number","minimum":0,"maximum":1},
    "ready":{"type":"boolean"}
  },
  "required":["name","count","ratio","ready"],
  "additionalProperties":false
}`
	template := `{
  "kind":"POSTGRESQL_STATEMENT",
  "postgresql":{"statements":[{"sql":"SELECT $1, $2, $3, $4","arguments":["name","count","ratio","ready"]}]}
}`
	tests := map[string][]byte{
		"not an object":       []byte(`[]`),
		"null object":         []byte(`null`),
		"duplicate key":       []byte(`{"name":"alpha","name":"beta","count":1,"ratio":0.5,"ready":true}`),
		"missing property":    []byte(`{"name":"alpha","count":1,"ratio":0.5}`),
		"extra property":      []byte(`{"name":"alpha","count":1,"ratio":0.5,"ready":true,"other":1}`),
		"null value":          []byte(`{"name":"alpha","count":1,"ratio":0.5,"ready":null}`),
		"string conversion":   []byte(`{"name":"alpha","count":"1","ratio":0.5,"ready":true}`),
		"decimal integer":     []byte(`{"name":"alpha","count":1.0,"ratio":0.5,"ready":true}`),
		"boolean conversion":  []byte(`{"name":"alpha","count":1,"ratio":0.5,"ready":"true"}`),
		"enum violation":      []byte(`{"name":"gamma","count":1,"ratio":0.5,"ready":true}`),
		"integer below bound": []byte(`{"name":"alpha","count":0,"ratio":0.5,"ready":true}`),
		"number above bound":  []byte(`{"name":"alpha","count":1,"ratio":1.1,"ready":true}`),
		"non-finite number":   []byte(`{"name":"alpha","count":1,"ratio":1e999,"ready":true}`),
		"lossy number":        []byte(`{"name":"alpha","count":1,"ratio":0.10000000000000001,"ready":true}`),
		"trailing content":    []byte(`{"name":"alpha","count":1,"ratio":0.5,"ready":true}{}`),
		"oversized document":  bytes.Repeat([]byte{' '}, MaxDocumentSize+1),
	}
	for name, arguments := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := Compile([]byte(schema), []byte(template), arguments)
			if !errors.Is(err, ErrInvalidArguments) {
				t.Fatalf("error = %v, want ErrInvalidArguments", err)
			}
		})
	}
}

func TestIntegerBoundsRemainExactAboveFloatPrecision(t *testing.T) {
	schema := `{
  "type":"object",
  "properties":{"value":{"type":"integer","minimum":9007199254740993}},
  "required":["value"],
  "additionalProperties":false
}`
	template := `{"kind":"POSTGRESQL_STATEMENT","postgresql":{"statements":[{"sql":"SELECT $1","arguments":["value"]}]}}`
	_, _, err := Compile([]byte(schema), []byte(template), []byte(`{"value":9007199254740992}`))
	if !errors.Is(err, ErrInvalidArguments) {
		t.Fatalf("error = %v, want exact bound rejection", err)
	}
}

func equalStrings(first, second []string) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func equalValues(first, second []any) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}
