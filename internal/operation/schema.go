package operation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxJSONDepth = 32
const maxNumberBytes = 128

type ValueType string

const (
	ValueString  ValueType = "string"
	ValueInteger ValueType = "integer"
	ValueNumber  ValueType = "number"
	ValueBoolean ValueType = "boolean"
)

// Schema is the deliberately small public schema dialect exposed to Agents.
// AdditionalProperties is a pointer so omission can be distinguished from the
// mandatory explicit value false.
type Schema struct {
	Type                 string              `json:"type"`
	Properties           map[string]Property `json:"properties"`
	Required             []string            `json:"required"`
	AdditionalProperties *bool               `json:"additionalProperties"`
}

// Property supports only constraints that can be validated identically by the
// control service without evaluating arbitrary JSON Schema features.
type Property struct {
	Type        ValueType    `json:"type"`
	Description string       `json:"description,omitempty"`
	Enum        []string     `json:"enum,omitempty"`
	Minimum     *json.Number `json:"minimum,omitempty"`
	Maximum     *json.Number `json:"maximum,omitempty"`
	MaxLength   *int         `json:"maxLength,omitempty"`
}

type compiledSchema struct {
	definition Schema
	required   map[string]struct{}
	properties map[string]compiledProperty
}

type compiledProperty struct {
	definition Property
	minimum    *float64
	maximum    *float64
	minimumInt *int64
	maximumInt *int64
}

func parseSchema(raw []byte) (compiledSchema, error) {
	var definition Schema
	value, err := decodeStrict(raw, ErrInvalidSchema, &definition)
	if err != nil {
		return compiledSchema{}, err
	}
	root, ok := value.(map[string]any)
	if !ok || hasNull(value) {
		return compiledSchema{}, invalid(ErrInvalidSchema, "schema must be a non-null object")
	}
	if err := requireExactKeys(root, ErrInvalidSchema, "type", "properties", "required", "additionalProperties"); err != nil {
		return compiledSchema{}, err
	}
	for _, requiredKey := range []string{"type", "properties", "required", "additionalProperties"} {
		if _, present := root[requiredKey]; !present {
			return compiledSchema{}, invalid(ErrInvalidSchema, "required schema keyword is missing")
		}
	}
	if definition.Type != "object" || definition.Properties == nil || definition.Required == nil ||
		definition.AdditionalProperties == nil || *definition.AdditionalProperties {
		return compiledSchema{}, invalid(ErrInvalidSchema, "root must be an object with additionalProperties false")
	}
	if len(definition.Properties) > MaxProperties {
		return compiledSchema{}, invalid(ErrInvalidSchema, "too many properties")
	}
	rawProperties, ok := root["properties"].(map[string]any)
	if !ok {
		return compiledSchema{}, invalid(ErrInvalidSchema, "properties must be an object")
	}
	for _, rawProperty := range rawProperties {
		propertyObject, ok := rawProperty.(map[string]any)
		if !ok {
			return compiledSchema{}, invalid(ErrInvalidSchema, "property must be an object")
		}
		if err := requireExactKeys(propertyObject, ErrInvalidSchema, "type", "description", "enum", "minimum", "maximum", "maxLength"); err != nil {
			return compiledSchema{}, err
		}
	}

	compiled := compiledSchema{
		definition: definition,
		required:   make(map[string]struct{}, len(definition.Required)),
		properties: make(map[string]compiledProperty, len(definition.Properties)),
	}
	for name, property := range definition.Properties {
		if name == "" || len(name) > 256 || strings.ContainsRune(name, '\x00') {
			return compiledSchema{}, invalid(ErrInvalidSchema, "invalid property name")
		}
		compiledProperty, err := validateProperty(property)
		if err != nil {
			return compiledSchema{}, err
		}
		compiled.properties[name] = compiledProperty
	}
	for _, name := range definition.Required {
		if _, duplicate := compiled.required[name]; duplicate {
			return compiledSchema{}, invalid(ErrInvalidSchema, "required contains a duplicate")
		}
		if _, exists := definition.Properties[name]; !exists {
			return compiledSchema{}, invalid(ErrInvalidSchema, "required references an unknown property")
		}
		compiled.required[name] = struct{}{}
	}
	return compiled, nil
}

func validateProperty(property Property) (compiledProperty, error) {
	result := compiledProperty{definition: property}
	switch property.Type {
	case ValueString:
		if property.Minimum != nil || property.Maximum != nil {
			return compiledProperty{}, invalid(ErrInvalidSchema, "string property has a numeric constraint")
		}
		if property.MaxLength != nil && (*property.MaxLength < 0 || *property.MaxLength > MaxStringBytes) {
			return compiledProperty{}, invalid(ErrInvalidSchema, "invalid maxLength")
		}
		seen := make(map[string]struct{}, len(property.Enum))
		if property.Enum != nil && len(property.Enum) == 0 {
			return compiledProperty{}, invalid(ErrInvalidSchema, "enum must not be empty")
		}
		for _, entry := range property.Enum {
			if len(entry) > MaxStringBytes {
				return compiledProperty{}, invalid(ErrInvalidSchema, "enum entry is too long")
			}
			if _, duplicate := seen[entry]; duplicate {
				return compiledProperty{}, invalid(ErrInvalidSchema, "enum contains a duplicate")
			}
			seen[entry] = struct{}{}
		}
	case ValueInteger, ValueNumber:
		if property.Enum != nil || property.MaxLength != nil {
			return compiledProperty{}, invalid(ErrInvalidSchema, "numeric property has a string constraint")
		}
		if property.Type == ValueInteger {
			minimum, err := parseIntegerBound(property.Minimum)
			if err != nil {
				return compiledProperty{}, err
			}
			maximum, err := parseIntegerBound(property.Maximum)
			if err != nil {
				return compiledProperty{}, err
			}
			if minimum != nil && maximum != nil && *minimum > *maximum {
				return compiledProperty{}, invalid(ErrInvalidSchema, "minimum exceeds maximum")
			}
			result.minimumInt, result.maximumInt = minimum, maximum
		} else {
			minimum, err := parseNumberBound(property.Minimum)
			if err != nil {
				return compiledProperty{}, err
			}
			maximum, err := parseNumberBound(property.Maximum)
			if err != nil {
				return compiledProperty{}, err
			}
			if minimum != nil && maximum != nil && *minimum > *maximum {
				return compiledProperty{}, invalid(ErrInvalidSchema, "minimum exceeds maximum")
			}
			result.minimum, result.maximum = minimum, maximum
		}
	case ValueBoolean:
		if property.Enum != nil || property.Minimum != nil || property.Maximum != nil || property.MaxLength != nil {
			return compiledProperty{}, invalid(ErrInvalidSchema, "boolean property has an incompatible constraint")
		}
	default:
		return compiledProperty{}, invalid(ErrInvalidSchema, "unsupported property type")
	}
	return result, nil
}

func parseIntegerBound(number *json.Number) (*int64, error) {
	if number == nil {
		return nil, nil
	}
	if !isIntegerLiteral(number.String()) {
		return nil, invalid(ErrInvalidSchema, "integer bound is not an integer literal")
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil {
		return nil, invalid(ErrInvalidSchema, "integer bound is out of range")
	}
	return &value, nil
}

func parseNumberBound(number *json.Number) (*float64, error) {
	if number == nil {
		return nil, nil
	}
	value, ok := exactFloat64(number.String())
	if !ok {
		return nil, invalid(ErrInvalidSchema, "number bound is not finite")
	}
	return &value, nil
}

func (schema compiledSchema) validateArguments(raw []byte) (map[string]any, []byte, error) {
	var object map[string]any
	value, err := decodeStrict(raw, ErrInvalidArguments, &object)
	if err != nil {
		return nil, nil, err
	}
	if value == nil || object == nil || hasNull(value) {
		return nil, nil, invalid(ErrInvalidArguments, "arguments must be a non-null object")
	}
	for name := range object {
		if _, declared := schema.properties[name]; !declared {
			return nil, nil, invalid(ErrInvalidArguments, "arguments contain an undeclared property")
		}
	}
	for name := range schema.required {
		if _, present := object[name]; !present {
			return nil, nil, invalid(ErrInvalidArguments, "a required property is missing")
		}
	}

	typed := make(map[string]any, len(object))
	for name, rawValue := range object {
		value, err := schema.properties[name].validateValue(rawValue)
		if err != nil {
			return nil, nil, err
		}
		typed[name] = value
	}
	canonical, err := json.Marshal(typed)
	if err != nil {
		return nil, nil, invalid(ErrInvalidArguments, "arguments cannot be canonicalized")
	}
	if len(canonical) > MaxDocumentSize {
		return nil, nil, invalid(ErrInvalidArguments, "canonical arguments are too large")
	}
	return typed, canonical, nil
}

func (property compiledProperty) validateValue(raw any) (any, error) {
	switch property.definition.Type {
	case ValueString:
		value, ok := raw.(string)
		if !ok {
			return nil, invalid(ErrInvalidArguments, "property type does not match schema")
		}
		if len(value) > MaxStringBytes {
			return nil, invalid(ErrInvalidArguments, "string property is too long")
		}
		if property.definition.MaxLength != nil && utf8.RuneCountInString(value) > *property.definition.MaxLength {
			return nil, invalid(ErrInvalidArguments, "string exceeds maxLength")
		}
		if property.definition.Enum != nil {
			matched := false
			for _, allowed := range property.definition.Enum {
				matched = matched || value == allowed
			}
			if !matched {
				return nil, invalid(ErrInvalidArguments, "string is not in enum")
			}
		}
		return value, nil
	case ValueInteger:
		number, ok := raw.(json.Number)
		if !ok || !isIntegerLiteral(number.String()) {
			return nil, invalid(ErrInvalidArguments, "property type does not match schema")
		}
		value, err := strconv.ParseInt(number.String(), 10, 64)
		if err != nil {
			return nil, invalid(ErrInvalidArguments, "integer is out of range")
		}
		if property.minimumInt != nil && value < *property.minimumInt || property.maximumInt != nil && value > *property.maximumInt {
			return nil, invalid(ErrInvalidArguments, "integer is outside bounds")
		}
		return value, nil
	case ValueNumber:
		number, ok := raw.(json.Number)
		if !ok {
			return nil, invalid(ErrInvalidArguments, "property type does not match schema")
		}
		value, ok := exactFloat64(number.String())
		if !ok {
			return nil, invalid(ErrInvalidArguments, "number is not finite")
		}
		if !withinBounds(value, property.minimum, property.maximum) {
			return nil, invalid(ErrInvalidArguments, "number is outside bounds")
		}
		if value == 0 {
			value = 0 // canonicalize negative zero
		}
		return value, nil
	case ValueBoolean:
		value, ok := raw.(bool)
		if !ok {
			return nil, invalid(ErrInvalidArguments, "property type does not match schema")
		}
		return value, nil
	default:
		return nil, invalid(ErrInvalidSchema, "unsupported property type")
	}
}

func withinBounds(value float64, minimum, maximum *float64) bool {
	return (minimum == nil || value >= *minimum) && (maximum == nil || value <= *maximum)
}

// exactFloat64 accepts decimal spellings whose shortest float64 spelling has
// the same mathematical value. This rejects silent precision collapse such as
// 9007199254740993 becoming 9007199254740992 while still accepting ordinary
// values such as 0.1 and equivalent exponent notation.
func exactFloat64(literal string) (float64, bool) {
	if len(literal) == 0 || len(literal) > maxNumberBytes {
		return 0, false
	}
	value, err := strconv.ParseFloat(literal, 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, false
	}
	original, ok := new(big.Rat).SetString(literal)
	if !ok {
		return 0, false
	}
	canonicalLiteral := strconv.FormatFloat(value, 'g', -1, 64)
	canonical, ok := new(big.Rat).SetString(canonicalLiteral)
	if !ok || original.Cmp(canonical) != 0 {
		return 0, false
	}
	return value, true
}

func isIntegerLiteral(value string) bool {
	if value == "0" {
		return true
	}
	if strings.HasPrefix(value, "-") {
		value = strings.TrimPrefix(value, "-")
	}
	if value == "" || value[0] == '0' {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func decodeStrict(raw []byte, sentinel error, destination any) (any, error) {
	return decodeStrictDocument(raw, MaxDocumentSize, MaxStringBytes, sentinel, destination)
}

func decodeStrictDocument(raw []byte, maxDocumentSize, maxStringBytes int, sentinel error, destination any) (any, error) {
	if len(raw) == 0 || maxDocumentSize > 0 && len(raw) > maxDocumentSize || !utf8.Valid(raw) {
		return nil, invalid(sentinel, "JSON document has an invalid size or encoding")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := readJSONValue(decoder, 0, maxStringBytes)
	if err != nil {
		return nil, invalid(sentinel, "JSON document is malformed")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, invalid(sentinel, "JSON document has trailing content")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, invalid(sentinel, "JSON document cannot be decoded")
	}
	strict := json.NewDecoder(bytes.NewReader(canonical))
	strict.UseNumber()
	strict.DisallowUnknownFields()
	if err := strict.Decode(destination); err != nil {
		return nil, invalid(sentinel, "JSON document has unknown or invalid fields")
	}
	return value, nil
}

func readJSONValue(decoder *json.Decoder, depth, maxStringBytes int) (any, error) {
	if depth > maxJSONDepth {
		return nil, errors.New("maximum JSON depth exceeded")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token := token.(type) {
	case json.Delim:
		switch token {
		case '{':
			result := make(map[string]any)
			for decoder.More() {
				nameToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				name, ok := nameToken.(string)
				if !ok || maxStringBytes > 0 && len(name) > maxStringBytes {
					return nil, errors.New("invalid object key")
				}
				if _, duplicate := result[name]; duplicate {
					return nil, errors.New("duplicate object key")
				}
				value, err := readJSONValue(decoder, depth+1, maxStringBytes)
				if err != nil {
					return nil, err
				}
				result[name] = value
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim('}') {
				return nil, errors.New("unterminated object")
			}
			return result, nil
		case '[':
			result := make([]any, 0)
			for decoder.More() {
				value, err := readJSONValue(decoder, depth+1, maxStringBytes)
				if err != nil {
					return nil, err
				}
				result = append(result, value)
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim(']') {
				return nil, errors.New("unterminated array")
			}
			return result, nil
		default:
			return nil, errors.New("unexpected delimiter")
		}
	case string:
		if maxStringBytes > 0 && len(token) > maxStringBytes {
			return nil, errors.New("string is too long")
		}
		return token, nil
	case json.Number, bool, nil:
		return token, nil
	default:
		return nil, fmt.Errorf("unexpected JSON token")
	}
}

func hasNull(value any) bool {
	switch value := value.(type) {
	case nil:
		return true
	case []any:
		for _, entry := range value {
			if hasNull(entry) {
				return true
			}
		}
	case map[string]any:
		for _, entry := range value {
			if hasNull(entry) {
				return true
			}
		}
	}
	return false
}

func invalid(sentinel error, message string) error {
	return fmt.Errorf("%w: %s", sentinel, message)
}

func requireExactKeys(object map[string]any, sentinel error, allowed ...string) error {
	set := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		set[name] = struct{}{}
	}
	for name := range object {
		if _, exists := set[name]; !exists {
			return invalid(sentinel, "JSON object has an unknown or incorrectly cased field")
		}
	}
	return nil
}
