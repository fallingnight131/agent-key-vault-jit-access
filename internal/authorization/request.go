package authorization

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/fallingnight/akv/internal/agent"
	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/domain"
	"github.com/fallingnight/akv/internal/task"
)

const ApprovalWait = 30 * time.Minute

var (
	ErrInvalidRequest = errors.New("invalid authorization request")
	ErrContextDenied  = errors.New("authorization context denied")
)

type OperationKind string

const (
	OperationHTTP                OperationKind = "HTTP"
	OperationPostgreSQLStatement OperationKind = "POSTGRESQL_STATEMENT"
	OperationPostgreSQLBatch     OperationKind = "POSTGRESQL_TRANSACTION"
	OperationSign                OperationKind = "SIGN"
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

type Operation struct {
	Kind       OperationKind         `json:"kind"`
	HTTP       *HTTPParameters       `json:"http,omitempty"`
	PostgreSQL *PostgreSQLParameters `json:"postgresql,omitempty"`
	Sign       *SignParameters       `json:"sign,omitempty"`
}

type SubmitInput struct {
	TaskID    string    `json:"task_id"`
	TargetID  string    `json:"target_id"`
	Operation Operation `json:"operation"`
	Reason    string    `json:"reason"`
}

type Request struct {
	ID                string
	AgentID           string
	TaskID            string
	TargetID          string
	CredentialID      string
	OperationKind     OperationKind
	OperationSnapshot []byte
	OperationHash     [sha256.Size]byte
	Reason            string
	Status            domain.RequestStatus
	CreatedAt         time.Time
	ApprovalDeadline  time.Time
}

type TaskValidator interface {
	ValidateActive(context.Context, string, string) (task.Record, error)
}

type CatalogResolver interface {
	ResolveForRequest(context.Context, string) (catalog.Target, catalog.Credential, error)
}

type Repository interface {
	CreateRequest(context.Context, Request) error
}

type Service struct {
	tasks      TaskValidator
	catalog    CatalogResolver
	repository Repository
	now        func() time.Time
	newID      func() (string, error)
}

func NewService(tasks TaskValidator, catalogResolver CatalogResolver, repository Repository) *Service {
	return &Service{tasks: tasks, catalog: catalogResolver, repository: repository, now: time.Now, newID: randomID}
}

func (service *Service) Submit(ctx context.Context, principal agent.Principal, input SubmitInput) (Request, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if principal.AgentID == "" || input.TaskID == "" || input.TargetID == "" || input.Reason == "" {
		return Request{}, ErrInvalidRequest
	}
	if _, err := service.tasks.ValidateActive(ctx, principal.AgentID, input.TaskID); err != nil {
		return Request{}, ErrContextDenied
	}
	target, credential, err := service.catalog.ResolveForRequest(ctx, input.TargetID)
	if err != nil {
		return Request{}, ErrContextDenied
	}
	if err := validateOperation(target, credential, input.Operation); err != nil {
		return Request{}, err
	}
	operationSnapshot, err := json.Marshal(input.Operation)
	if err != nil {
		return Request{}, ErrInvalidRequest
	}
	operationHash, err := hashSnapshot(principal.AgentID, input.TaskID, target.ID, credential.ID, operationSnapshot)
	if err != nil {
		return Request{}, fmt.Errorf("hash authorization snapshot: %w", err)
	}
	id, err := service.newID()
	if err != nil {
		return Request{}, fmt.Errorf("generate request ID: %w", err)
	}
	now := service.now()
	request := Request{
		ID: id, AgentID: principal.AgentID, TaskID: input.TaskID,
		TargetID: target.ID, CredentialID: credential.ID,
		OperationKind: input.Operation.Kind, OperationSnapshot: operationSnapshot,
		OperationHash: operationHash, Reason: input.Reason,
		Status: domain.RequestPendingApproval, CreatedAt: now, ApprovalDeadline: now.Add(ApprovalWait),
	}
	if err := service.repository.CreateRequest(ctx, request); err != nil {
		return Request{}, fmt.Errorf("create authorization request: %w", err)
	}
	return request, nil
}

func validateOperation(target catalog.Target, credential catalog.Credential, operation Operation) error {
	switch operation.Kind {
	case OperationHTTP:
		if target.ConnectorType != catalog.ConnectorHTTP || operation.HTTP == nil || operation.PostgreSQL != nil || operation.Sign != nil {
			return ErrInvalidRequest
		}
		if !slices.Contains([]catalog.CredentialType{catalog.CredentialAPIKey, catalog.CredentialAccessToken, catalog.CredentialUsernamePassword}, credential.Type) {
			return ErrInvalidRequest
		}
		parameters := operation.HTTP
		parameters.Method = strings.ToUpper(strings.TrimSpace(parameters.Method))
		if !slices.Contains(target.ConnectionConfig.AllowedHTTPMethods, parameters.Method) {
			return ErrInvalidRequest
		}
		parsed, err := url.ParseRequestURI(parameters.Path)
		if err != nil || !strings.HasPrefix(parameters.Path, "/") || parsed.IsAbs() || parsed.Host != "" {
			return ErrInvalidRequest
		}
		for name := range parameters.Headers {
			canonical := http.CanonicalHeaderKey(name)
			if slices.Contains([]string{"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie", "X-Api-Key"}, canonical) {
				return ErrInvalidRequest
			}
		}
	case OperationPostgreSQLStatement, OperationPostgreSQLBatch:
		if target.ConnectorType != catalog.ConnectorPostgreSQL || operation.PostgreSQL == nil || operation.HTTP != nil || operation.Sign != nil {
			return ErrInvalidRequest
		}
		statements := operation.PostgreSQL.Statements
		if len(statements) == 0 || operation.Kind == OperationPostgreSQLStatement && len(statements) != 1 {
			return ErrInvalidRequest
		}
		for _, statement := range statements {
			if strings.TrimSpace(statement.SQL) == "" || strings.Contains(statement.SQL, "\x00") {
				return ErrInvalidRequest
			}
		}
	case OperationSign:
		if credential.Type != catalog.CredentialTransitKey || operation.Sign == nil || operation.HTTP != nil || operation.PostgreSQL != nil || len(operation.Sign.Digest) == 0 {
			return ErrInvalidRequest
		}
		if !slices.Contains([]string{"sha2-256", "sha2-384", "sha2-512"}, operation.Sign.DigestAlgorithm) {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	return nil
}

func hashSnapshot(agentID, taskID, targetID, credentialID string, operation []byte) ([sha256.Size]byte, error) {
	encoded, err := json.Marshal(struct {
		AgentID      string          `json:"agent_id"`
		TaskID       string          `json:"task_id"`
		TargetID     string          `json:"target_id"`
		CredentialID string          `json:"credential_id"`
		Operation    json.RawMessage `json:"operation"`
	}{agentID, taskID, targetID, credentialID, operation})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
