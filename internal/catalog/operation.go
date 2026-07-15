package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"
)

type ExecutorType string

const (
	ExecutorHTTP       ExecutorType = "HTTP"
	ExecutorPostgreSQL ExecutorType = "POSTGRESQL"
	ExecutorSign       ExecutorType = "SIGN"
)

type RiskLevel string

const (
	RiskLow    RiskLevel = "LOW"
	RiskMedium RiskLevel = "MEDIUM"
	RiskHigh   RiskLevel = "HIGH"
)

type OperationSet struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	ExecutorType ExecutorType `json:"executor_type"`
	Active       bool         `json:"active"`
	CreatedBy    string       `json:"created_by"`
	UpdatedBy    string       `json:"updated_by"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

type SafeOperation struct {
	ID             string    `json:"id"`
	OperationSetID string    `json:"operation_set_id"`
	Key            string    `json:"key"`
	CurrentVersion int       `json:"current_version"`
	Active         bool      `json:"active"`
	CreatedBy      string    `json:"created_by"`
	UpdatedBy      string    `json:"updated_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type OperationVersion struct {
	OperationID       string            `json:"operation_id"`
	Version           int               `json:"version"`
	Name              string            `json:"name"`
	Description       string            `json:"description"`
	Kind              string            `json:"operation_kind"`
	RiskLevel         RiskLevel         `json:"risk_level"`
	ArgumentsSchema   json.RawMessage   `json:"arguments_schema"`
	ExecutionTemplate json.RawMessage   `json:"execution_template"`
	DefinitionHash    [sha256.Size]byte `json:"-"`
	CreatedBy         string            `json:"created_by"`
	CreatedAt         time.Time         `json:"created_at"`
}

type TargetOperationBinding struct {
	TargetID    string          `json:"target_id"`
	OperationID string          `json:"operation_id"`
	Version     int             `json:"version"`
	Active      bool            `json:"active"`
	Policy      json.RawMessage `json:"policy"`
	CreatedBy   string          `json:"created_by"`
	UpdatedBy   string          `json:"updated_by"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type OperationCatalog struct {
	Sets       []OperationSet
	Operations []SafeOperation
	Versions   []OperationVersion
	Bindings   []TargetOperationBinding
}

type PublicOperation struct {
	OperationID     string          `json:"operation_id"`
	Version         int             `json:"version"`
	TargetID        string          `json:"target_id"`
	Key             string          `json:"key"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	Kind            string          `json:"operation_kind"`
	RiskLevel       RiskLevel       `json:"risk_level"`
	ArgumentsSchema json.RawMessage `json:"arguments_schema"`
}

type ResolvedOperation struct {
	Target     Target
	Credential Credential
	Set        OperationSet
	Operation  SafeOperation
	Version    OperationVersion
}

type CreateOperationSetInput struct {
	Name         string
	Description  string
	ExecutorType ExecutorType
}

type PublishOperationInput struct {
	Key               string
	Name              string
	Description       string
	RiskLevel         RiskLevel
	ArgumentsSchema   json.RawMessage
	ExecutionTemplate json.RawMessage
}

type OperationRepository interface {
	ListOperationCatalog(context.Context) (OperationCatalog, error)
	CreateOperationSet(context.Context, OperationSet) error
	CreateOperationWithVersion(context.Context, SafeOperation, OperationVersion) error
	PublishOperationVersion(context.Context, OperationVersion, int, string, time.Time) error
	SetOperationSetActive(context.Context, string, bool, string, time.Time) error
	SetOperationActive(context.Context, string, bool, string, time.Time) error
	UpsertTargetOperationBinding(context.Context, TargetOperationBinding) error
	FindOperationVersion(context.Context, string, int) (OperationSet, SafeOperation, OperationVersion, error)
	ListActiveTargetOperations(context.Context, string) ([]PublicOperation, error)
	FindActiveOperationForRequest(context.Context, string, string, int) (ResolvedOperation, error)
}
