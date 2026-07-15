package operation

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const maxSQLStatements = 32

// Template is private catalog data. Only its corresponding Schema is exposed
// to an Agent.
type Template struct {
	Kind       OperationKind       `json:"kind"`
	HTTP       *HTTPTemplate       `json:"http,omitempty"`
	PostgreSQL *PostgreSQLTemplate `json:"postgresql,omitempty"`
	Sign       *SignTemplate       `json:"sign,omitempty"`
}

type HTTPTemplate struct {
	Method         string            `json:"method"`
	Path           string            `json:"path"`
	PathArguments  map[string]string `json:"path_arguments,omitempty"`
	QueryArguments map[string]string `json:"query_arguments,omitempty"`
	StaticHeaders  map[string]string `json:"static_headers,omitempty"`
	BodyArguments  []string          `json:"body_arguments,omitempty"`
}

type PostgreSQLTemplate struct {
	Statements []SQLTemplate `json:"statements"`
}

type SQLTemplate struct {
	SQL       string   `json:"sql"`
	Arguments []string `json:"arguments,omitempty"`
}

type SignTemplate struct {
	Algorithm      string `json:"algorithm"`
	DigestArgument string `json:"digest_argument"`
}

type compiledTemplate struct {
	definition Template
	path       []pathSegment
}

type pathSegment struct {
	literal  string
	argument string
}

func parseTemplate(raw []byte, schema compiledSchema) (compiledTemplate, error) {
	var definition Template
	value, err := decodeStrict(raw, ErrInvalidTemplate, &definition)
	if err != nil {
		return compiledTemplate{}, err
	}
	if _, ok := value.(map[string]any); !ok || hasNull(value) {
		return compiledTemplate{}, invalid(ErrInvalidTemplate, "template must be a non-null object")
	}
	root := value.(map[string]any)
	if err := validateTemplateKeys(root); err != nil {
		return compiledTemplate{}, err
	}

	result := compiledTemplate{definition: definition}
	used := make(map[string]struct{}, len(schema.properties))
	switch definition.Kind {
	case OperationHTTP:
		if definition.HTTP == nil || definition.PostgreSQL != nil || definition.Sign != nil {
			return compiledTemplate{}, invalid(ErrInvalidTemplate, "HTTP template has an invalid shape")
		}
		path, err := validateHTTPTemplate(definition.HTTP, schema, used)
		if err != nil {
			return compiledTemplate{}, err
		}
		result.path = path
	case OperationPostgreSQLStatement, OperationPostgreSQLTransaction:
		if definition.PostgreSQL == nil || definition.HTTP != nil || definition.Sign != nil {
			return compiledTemplate{}, invalid(ErrInvalidTemplate, "PostgreSQL template has an invalid shape")
		}
		if err := validatePostgreSQLTemplate(definition.Kind, definition.PostgreSQL, schema, used); err != nil {
			return compiledTemplate{}, err
		}
	case OperationSign:
		if definition.Sign == nil || definition.HTTP != nil || definition.PostgreSQL != nil {
			return compiledTemplate{}, invalid(ErrInvalidTemplate, "sign template has an invalid shape")
		}
		if err := validateSignTemplate(definition.Sign, schema, used); err != nil {
			return compiledTemplate{}, err
		}
	default:
		return compiledTemplate{}, invalid(ErrInvalidTemplate, "unsupported operation kind")
	}
	for name := range schema.properties {
		if _, referenced := used[name]; !referenced {
			return compiledTemplate{}, invalid(ErrInvalidTemplate, "schema contains an unbound property")
		}
	}
	return result, nil
}

func validateHTTPTemplate(template *HTTPTemplate, schema compiledSchema, used map[string]struct{}) ([]pathSegment, error) {
	template.Method = strings.ToUpper(strings.TrimSpace(template.Method))
	switch template.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
	default:
		return nil, invalid(ErrInvalidTemplate, "unsupported HTTP method")
	}
	segments, placeholders, err := validatePath(template.Path)
	if err != nil {
		return nil, err
	}
	if len(placeholders) != len(template.PathArguments) {
		return nil, invalid(ErrInvalidTemplate, "path argument bindings do not match placeholders")
	}
	if len(template.PathArguments)+len(template.QueryArguments)+len(template.BodyArguments) > MaxBindings {
		return nil, invalid(ErrInvalidTemplate, "too many HTTP argument bindings")
	}
	for placeholder, argument := range template.PathArguments {
		index, exists := placeholders[placeholder]
		if !exists {
			return nil, invalid(ErrInvalidTemplate, "path binding references an unknown placeholder")
		}
		property, err := requireReference(schema, argument, true, used)
		if err != nil {
			return nil, err
		}
		if property.definition.Type != ValueString {
			return nil, invalid(ErrInvalidTemplate, "path arguments must be strings")
		}
		segments[index].argument = argument
	}

	for queryName, argument := range template.QueryArguments {
		if queryName == "" || strings.ContainsAny(queryName, "\x00\r\n") {
			return nil, invalid(ErrInvalidTemplate, "invalid query parameter name")
		}
		if _, err := requireReference(schema, argument, false, used); err != nil {
			return nil, err
		}
	}
	canonicalHeaders := make(map[string]struct{}, len(template.StaticHeaders))
	for name, value := range template.StaticHeaders {
		canonical := http.CanonicalHeaderKey(name)
		if !validHeaderName(name) || strings.ContainsAny(value, "\x00\r\n") || forbiddenHeader(canonical) {
			return nil, invalid(ErrInvalidTemplate, "unsafe static HTTP header")
		}
		if _, duplicate := canonicalHeaders[canonical]; duplicate {
			return nil, invalid(ErrInvalidTemplate, "duplicate HTTP header name")
		}
		canonicalHeaders[canonical] = struct{}{}
	}
	seenBody := make(map[string]struct{}, len(template.BodyArguments))
	for _, argument := range template.BodyArguments {
		if _, duplicate := seenBody[argument]; duplicate {
			return nil, invalid(ErrInvalidTemplate, "duplicate body argument")
		}
		seenBody[argument] = struct{}{}
		if _, err := requireReference(schema, argument, false, used); err != nil {
			return nil, err
		}
	}
	return segments, nil
}

func validatePath(path string) ([]pathSegment, map[string]int, error) {
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") ||
		strings.ContainsAny(path, "?#\\\x00\r\n%") {
		return nil, nil, invalid(ErrInvalidTemplate, "HTTP path must be a safe absolute path")
	}
	parts := strings.Split(path, "/")
	segments := make([]pathSegment, len(parts))
	placeholders := make(map[string]int)
	for index, part := range parts {
		segments[index].literal = part
		if index == 0 || part == "" && index == len(parts)-1 {
			continue
		}
		if part == "" || part == "." || part == ".." {
			return nil, nil, invalid(ErrInvalidTemplate, "HTTP path contains an unsafe segment")
		}
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			placeholder := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
			if placeholder == "" || strings.ContainsAny(placeholder, "{}") {
				return nil, nil, invalid(ErrInvalidTemplate, "invalid path placeholder")
			}
			if _, duplicate := placeholders[placeholder]; duplicate {
				return nil, nil, invalid(ErrInvalidTemplate, "duplicate path placeholder")
			}
			placeholders[placeholder] = index
			segments[index].literal = ""
		} else if strings.ContainsAny(part, "{}") {
			return nil, nil, invalid(ErrInvalidTemplate, "path placeholders must occupy a complete segment")
		}
	}
	return segments, placeholders, nil
}

func validatePostgreSQLTemplate(kind OperationKind, template *PostgreSQLTemplate, schema compiledSchema, used map[string]struct{}) error {
	if len(template.Statements) == 0 || len(template.Statements) > maxSQLStatements ||
		kind == OperationPostgreSQLStatement && len(template.Statements) != 1 {
		return invalid(ErrInvalidTemplate, "invalid PostgreSQL statement count")
	}
	totalBindings := 0
	for _, statement := range template.Statements {
		if strings.TrimSpace(statement.SQL) == "" || strings.ContainsAny(statement.SQL, ";\x00") {
			return invalid(ErrInvalidTemplate, "SQL must be one non-empty statement without a terminator")
		}
		for _, argument := range statement.Arguments {
			totalBindings++
			if totalBindings > MaxBindings {
				return invalid(ErrInvalidTemplate, "too many PostgreSQL argument bindings")
			}
			if _, err := requireReference(schema, argument, true, used); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSignTemplate(template *SignTemplate, schema compiledSchema, used map[string]struct{}) error {
	if digestLength(template.Algorithm) == 0 {
		return invalid(ErrInvalidTemplate, "unsupported digest algorithm")
	}
	property, err := requireReference(schema, template.DigestArgument, true, used)
	if err != nil {
		return err
	}
	if property.definition.Type != ValueString {
		return invalid(ErrInvalidTemplate, "digest argument must be a string")
	}
	return nil
}

func requireReference(schema compiledSchema, name string, required bool, used map[string]struct{}) (compiledProperty, error) {
	property, exists := schema.properties[name]
	if name == "" || !exists {
		return compiledProperty{}, invalid(ErrInvalidTemplate, "template references an undeclared argument")
	}
	if required {
		if _, present := schema.required[name]; !present {
			return compiledProperty{}, invalid(ErrInvalidTemplate, "binding requires an optional argument")
		}
	}
	used[name] = struct{}{}
	return property, nil
}

func (template compiledTemplate) materialize(arguments map[string]any) (ResolvedOperation, error) {
	switch template.definition.Kind {
	case OperationHTTP:
		return template.materializeHTTP(arguments)
	case OperationPostgreSQLStatement, OperationPostgreSQLTransaction:
		statements := make([]SQLStatement, 0, len(template.definition.PostgreSQL.Statements))
		for _, definition := range template.definition.PostgreSQL.Statements {
			values := make([]any, 0, len(definition.Arguments))
			for _, name := range definition.Arguments {
				values = append(values, arguments[name])
			}
			statements = append(statements, SQLStatement{SQL: definition.SQL, Arguments: values})
		}
		return ResolvedOperation{Kind: template.definition.Kind, PostgreSQL: &PostgreSQLParameters{Statements: statements}}, nil
	case OperationSign:
		encoded, ok := arguments[template.definition.Sign.DigestArgument].(string)
		if !ok {
			return ResolvedOperation{}, invalid(ErrInvalidArguments, "digest argument is missing")
		}
		digest, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || base64.StdEncoding.EncodeToString(digest) != encoded || len(digest) != digestLength(template.definition.Sign.Algorithm) {
			return ResolvedOperation{}, invalid(ErrInvalidArguments, "digest is not canonical base64 of the required length")
		}
		return ResolvedOperation{Kind: OperationSign, Sign: &SignParameters{
			DigestAlgorithm: template.definition.Sign.Algorithm,
			Digest:          digest,
		}}, nil
	default:
		return ResolvedOperation{}, invalid(ErrInvalidTemplate, "unsupported operation kind")
	}
}

func (template compiledTemplate) materializeHTTP(arguments map[string]any) (ResolvedOperation, error) {
	parts := make([]string, len(template.path))
	for index, segment := range template.path {
		if segment.argument == "" {
			parts[index] = segment.literal
			continue
		}
		value, ok := arguments[segment.argument].(string)
		if !ok || unsafePathArgument(value) {
			return ResolvedOperation{}, invalid(ErrInvalidArguments, "unsafe path argument")
		}
		parts[index] = url.PathEscape(value)
	}
	parameters := &HTTPParameters{
		Method:  template.definition.HTTP.Method,
		Path:    strings.Join(parts, "/"),
		Query:   make(map[string][]string),
		Headers: make(map[string]string, len(template.definition.HTTP.StaticHeaders)),
	}
	for name, argument := range template.definition.HTTP.QueryArguments {
		if value, present := arguments[argument]; present {
			parameters.Query[name] = []string{formatPrimitive(value)}
		}
	}
	for name, value := range template.definition.HTTP.StaticHeaders {
		parameters.Headers[http.CanonicalHeaderKey(name)] = value
	}
	if len(template.definition.HTTP.BodyArguments) != 0 {
		body := make(map[string]any, len(template.definition.HTTP.BodyArguments))
		for _, name := range template.definition.HTTP.BodyArguments {
			if value, present := arguments[name]; present {
				body[name] = value
			}
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			return ResolvedOperation{}, invalid(ErrInvalidArguments, "HTTP body cannot be encoded")
		}
		parameters.Body = encoded
	}
	if len(parameters.Query) == 0 {
		parameters.Query = nil
	}
	if len(parameters.Headers) == 0 {
		parameters.Headers = nil
	}
	return ResolvedOperation{Kind: OperationHTTP, HTTP: parameters}, nil
}

func unsafePathArgument(value string) bool {
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\%") {
		return true
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func formatPrimitive(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return strconv.FormatFloat(value, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(value)
	default:
		return ""
	}
}

func digestLength(algorithm string) int {
	switch algorithm {
	case "sha2-256":
		return 32
	case "sha2-384":
		return 48
	case "sha2-512":
		return 64
	default:
		return 0
	}
}

func forbiddenHeader(name string) bool {
	switch name {
	case "Authorization", "Proxy-Authorization", "Host", "Cookie", "Set-Cookie", "X-Api-Key",
		"Connection", "Content-Length", "Keep-Alive", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, character := range name {
		if character > 127 || !strings.ContainsRune("!#$%&'*+-.^_`|~0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", character) {
			return false
		}
	}
	return true
}

func validateTemplateKeys(root map[string]any) error {
	if err := requireExactKeys(root, ErrInvalidTemplate, "kind", "http", "postgresql", "sign"); err != nil {
		return err
	}
	if raw, present := root["http"]; present {
		object, ok := raw.(map[string]any)
		if !ok {
			return invalid(ErrInvalidTemplate, "http must be an object")
		}
		if err := requireExactKeys(object, ErrInvalidTemplate, "method", "path", "path_arguments", "query_arguments", "static_headers", "body_arguments"); err != nil {
			return err
		}
	}
	if raw, present := root["postgresql"]; present {
		object, ok := raw.(map[string]any)
		if !ok {
			return invalid(ErrInvalidTemplate, "postgresql must be an object")
		}
		if err := requireExactKeys(object, ErrInvalidTemplate, "statements"); err != nil {
			return err
		}
		if rawStatements, present := object["statements"]; present {
			statements, ok := rawStatements.([]any)
			if !ok {
				return invalid(ErrInvalidTemplate, "statements must be an array")
			}
			for _, rawStatement := range statements {
				statement, ok := rawStatement.(map[string]any)
				if !ok {
					return invalid(ErrInvalidTemplate, "statement must be an object")
				}
				if err := requireExactKeys(statement, ErrInvalidTemplate, "sql", "arguments"); err != nil {
					return err
				}
			}
		}
	}
	if raw, present := root["sign"]; present {
		object, ok := raw.(map[string]any)
		if !ok {
			return invalid(ErrInvalidTemplate, "sign must be an object")
		}
		if err := requireExactKeys(object, ErrInvalidTemplate, "algorithm", "digest_argument"); err != nil {
			return err
		}
	}
	return nil
}
