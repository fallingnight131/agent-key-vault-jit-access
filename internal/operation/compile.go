// Package operation compiles a small public argument schema and a private,
// administrator-owned template into an immutable connector operation.
package operation

import "encoding/json"

// ValidateDefinition verifies that a public schema and private template form a
// complete, safe definition. It does not accept or materialize Agent input.
func ValidateDefinition(schemaRaw, templateRaw []byte) error {
	schema, err := parseSchema(schemaRaw)
	if err != nil {
		return err
	}
	_, err = parseTemplate(templateRaw, schema)
	return err
}

// Compile strictly validates Agent arguments and resolves them through a
// pre-validated administrator template. canonicalArguments is stable JSON with
// sorted object keys and normalized primitive values.
func Compile(schemaRaw, templateRaw, argumentsRaw []byte) (operation ResolvedOperation, canonicalArguments []byte, err error) {
	schema, err := parseSchema(schemaRaw)
	if err != nil {
		return ResolvedOperation{}, nil, err
	}
	template, err := parseTemplate(templateRaw, schema)
	if err != nil {
		return ResolvedOperation{}, nil, err
	}
	arguments, canonical, err := schema.validateArguments(argumentsRaw)
	if err != nil {
		return ResolvedOperation{}, nil, err
	}
	resolved, err := template.materialize(arguments)
	if err != nil {
		return ResolvedOperation{}, nil, err
	}
	encoded, err := json.Marshal(resolved)
	if err != nil || len(encoded) > MaxResolvedOperationSize {
		return ResolvedOperation{}, nil, invalid(ErrInvalidArguments, "resolved operation is too large")
	}
	return resolved, canonical, nil
}
