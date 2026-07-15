package operation

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
)

var errInvalidSQLStatement = errors.New("invalid resolved SQL statement")

// MarshalJSON preserves integral float64 bind values as JSON numbers with a
// decimal point so UnmarshalJSON can distinguish schema `number` from schema
// `integer` after a frozen snapshot round trip.
func (statement SQLStatement) MarshalJSON() ([]byte, error) {
	arguments := make([]any, len(statement.Arguments))
	for index, argument := range statement.Arguments {
		arguments[index] = prepareSQLJSONValue(argument)
	}
	return json.Marshal(struct {
		SQL       string `json:"sql"`
		Arguments []any  `json:"arguments,omitempty"`
	}{SQL: statement.SQL, Arguments: arguments})
}

// UnmarshalJSON preserves the integer/number distinction needed by database
// bind values. encoding/json otherwise turns every number stored in an any into
// float64 when a frozen operation is loaded for execution.
func (statement *SQLStatement) UnmarshalJSON(data []byte) error {
	if statement == nil {
		return invalid(errInvalidSQLStatement, "nil destination")
	}
	var wire struct {
		SQL       string `json:"sql"`
		Arguments []any  `json:"arguments,omitempty"`
	}
	value, err := decodeStrictDocument(data, 0, 0, errInvalidSQLStatement, &wire)
	if err != nil {
		return err
	}
	root, ok := value.(map[string]any)
	if !ok {
		return invalid(errInvalidSQLStatement, "statement must be an object")
	}
	if hasNull(value) {
		return invalid(errInvalidSQLStatement, "statement contains null")
	}
	if err := requireExactKeys(root, errInvalidSQLStatement, "sql", "arguments"); err != nil {
		return err
	}
	if _, present := root["sql"]; !present {
		return invalid(errInvalidSQLStatement, "sql is missing")
	}
	for index, argument := range wire.Arguments {
		normalized, err := normalizeSQLJSONValue(argument)
		if err != nil {
			return err
		}
		wire.Arguments[index] = normalized
	}
	statement.SQL = wire.SQL
	statement.Arguments = wire.Arguments
	return nil
}

func normalizeSQLJSONValue(value any) (any, error) {
	switch value := value.(type) {
	case json.Number:
		if isIntegerLiteral(value.String()) {
			if integer, err := strconv.ParseInt(value.String(), 10, 64); err == nil {
				return integer, nil
			}
		}
		number, err := strconv.ParseFloat(value.String(), 64)
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return nil, invalid(errInvalidSQLStatement, "bind number is not finite")
		}
		return number, nil
	case string, bool:
		return value, nil
	case []any:
		for index, entry := range value {
			normalized, err := normalizeSQLJSONValue(entry)
			if err != nil {
				return nil, err
			}
			value[index] = normalized
		}
		return value, nil
	case map[string]any:
		for name, entry := range value {
			normalized, err := normalizeSQLJSONValue(entry)
			if err != nil {
				return nil, err
			}
			value[name] = normalized
		}
		return value, nil
	default:
		return nil, invalid(errInvalidSQLStatement, "unsupported bind value")
	}
}

type sqlJSONFloat float64

func (value sqlJSONFloat) MarshalJSON() ([]byte, error) {
	number := float64(value)
	if math.IsInf(number, 0) || math.IsNaN(number) {
		return nil, invalid(errInvalidSQLStatement, "bind number is not finite")
	}
	encoded := strconv.FormatFloat(number, 'g', -1, 64)
	if !stringsContainsAny(encoded, ".eE") {
		encoded += ".0"
	}
	return []byte(encoded), nil
}

func prepareSQLJSONValue(value any) any {
	switch value := value.(type) {
	case float64:
		return sqlJSONFloat(value)
	case []any:
		result := make([]any, len(value))
		for index, entry := range value {
			result[index] = prepareSQLJSONValue(entry)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(value))
		for name, entry := range value {
			result[name] = prepareSQLJSONValue(entry)
		}
		return result
	default:
		return value
	}
}

func stringsContainsAny(value, characters string) bool {
	return bytes.ContainsAny([]byte(value), characters)
}
