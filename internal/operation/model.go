package operation

import "errors"

const (
	// MaxDocumentSize limits every untrusted JSON document accepted by this
	// package. Keeping the limit here makes it shared by definition validation
	// and request compilation.
	MaxDocumentSize = 64 << 10
	MaxProperties   = 64
	MaxStringBytes  = 8 << 10
	MaxBindings     = 256

	// MaxResolvedOperationSize prevents a small template from amplifying one
	// large argument through thousands of repeated bindings before persistence.
	MaxResolvedOperationSize = 256 << 10
)

var (
	ErrInvalidSchema    = errors.New("invalid operation schema")
	ErrInvalidTemplate  = errors.New("invalid operation template")
	ErrInvalidArguments = errors.New("invalid operation arguments")
)

// OperationKind identifies the connector operation represented by a resolved
// execution snapshot.
type OperationKind string

const (
	OperationHTTP                  OperationKind = "HTTP"
	OperationPostgreSQLStatement   OperationKind = "POSTGRESQL_STATEMENT"
	OperationPostgreSQLTransaction OperationKind = "POSTGRESQL_TRANSACTION"
	OperationSign                  OperationKind = "SIGN"

	// OperationPostgreSQLBatch is retained as a source-compatible name for the
	// transaction kind used by the existing execution proxy.
	OperationPostgreSQLBatch = OperationPostgreSQLTransaction
)

type HTTPParameters struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Query   map[string][]string `json:"query,omitempty"`
	Headers map[string]string   `json:"headers,omitempty"`
	Body    []byte              `json:"body,omitempty"`
}

type SQLStatement struct {
	SQL       string `json:"sql"`
	Arguments []any  `json:"arguments,omitempty"`
}

type PostgreSQLParameters struct {
	Statements []SQLStatement `json:"statements"`
}

type SignParameters struct {
	DigestAlgorithm string `json:"digest_algorithm"`
	Digest          []byte `json:"digest"`
}

// ResolvedOperation is the private, immutable operation snapshot consumed by
// the execution proxy. It must never be accepted directly from an Agent.
type ResolvedOperation struct {
	Kind       OperationKind         `json:"kind"`
	HTTP       *HTTPParameters       `json:"http,omitempty"`
	PostgreSQL *PostgreSQLParameters `json:"postgresql,omitempty"`
	Sign       *SignParameters       `json:"sign,omitempty"`
}

// Operation is a convenience alias for callers migrating the pre-template
// authorization model.
type Operation = ResolvedOperation
