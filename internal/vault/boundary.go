package vault

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrUnavailable   = errors.New("vault unavailable")
	ErrNotExecutable = errors.New("credential is not executable")
)

// ControlWriter is the complete OpenBao capability available to the control
// plane. It intentionally contains no read, sign, issue, or revoke operation.
type ControlWriter interface {
	WriteKV(context.Context, KVWrite) error
	ConfigureDatabaseRole(context.Context, DatabaseRole) error
}

type KVWrite struct {
	Path   string
	Values map[string]*SensitiveValue
}

type DatabaseRole struct {
	Name               string
	ConnectionName     string
	CreationStatements []string
	DefaultTTL         time.Duration
	MaxTTL             time.Duration
}

// ExecutionClient is the capability used only by the execution proxy.
type ExecutionClient interface {
	ReadKV(context.Context, string, *int) (map[string]*SensitiveValue, error)
	Sign(context.Context, string, string, []byte) ([]byte, error)
	IssueDatabase(context.Context, string, time.Duration) (DynamicCredential, error)
	RevokeLease(context.Context, string) error
}

type DynamicCredential struct {
	Username  *SensitiveValue
	Password  *SensitiveValue
	LeaseID   string
	ExpiresAt time.Time
}

// SensitiveValue prevents accidental formatting and supports deterministic
// in-memory cleanup after connector use.
type SensitiveValue struct {
	value []byte
}

func NewSensitiveValue(value []byte) *SensitiveValue {
	return &SensitiveValue{value: append([]byte(nil), value...)}
}

func (value *SensitiveValue) String() string { return "[REDACTED]" }

func (value *SensitiveValue) GoString() string { return "[REDACTED]" }

func (value *SensitiveValue) WithBytes(use func([]byte) error) error {
	if value == nil || value.value == nil {
		return ErrUnavailable
	}
	return use(value.value)
}

func (value *SensitiveValue) Destroy() {
	if value == nil {
		return
	}
	for index := range value.value {
		value.value[index] = 0
	}
	value.value = nil
}

type ReferenceKind string

const (
	ReferenceStaticKV          ReferenceKind = "STATIC_KV"
	ReferencePostgreSQLDynamic ReferenceKind = "POSTGRESQL_DYNAMIC"
	ReferenceTransit           ReferenceKind = "TRANSIT"
	ReferenceCertificate       ReferenceKind = "CERTIFICATE"
)

type Reference struct {
	Kind    ReferenceKind
	Path    string
	Version *int
	TTL     time.Duration
}

type Handle struct {
	Values  map[string]*SensitiveValue
	LeaseID string
	client  ExecutionClient
}

// Acquire never falls back from a required dynamic credential to static KV.
func Acquire(ctx context.Context, client ExecutionClient, reference Reference) (*Handle, error) {
	switch reference.Kind {
	case ReferenceStaticKV:
		values, err := client.ReadKV(ctx, reference.Path, reference.Version)
		if err != nil {
			return nil, fmt.Errorf("read static credential: %w", err)
		}
		return &Handle{Values: values}, nil
	case ReferencePostgreSQLDynamic:
		credential, err := client.IssueDatabase(ctx, reference.Path, reference.TTL)
		if err != nil {
			return nil, fmt.Errorf("issue dynamic credential: %w", err)
		}
		return &Handle{
			Values:  map[string]*SensitiveValue{"username": credential.Username, "password": credential.Password},
			LeaseID: credential.LeaseID, client: client,
		}, nil
	case ReferenceTransit, ReferenceCertificate:
		return nil, ErrNotExecutable
	default:
		return nil, ErrUnavailable
	}
}

func (handle *Handle) Close(ctx context.Context) error {
	if handle == nil {
		return nil
	}
	for _, value := range handle.Values {
		value.Destroy()
	}
	handle.Values = nil
	if handle.LeaseID != "" {
		if err := handle.client.RevokeLease(ctx, handle.LeaseID); err != nil {
			return fmt.Errorf("revoke dynamic credential lease: %w", err)
		}
		handle.LeaseID = ""
	}
	return nil
}
