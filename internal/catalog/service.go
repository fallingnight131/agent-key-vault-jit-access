package catalog

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/fallingnight/akv/internal/identity"
)

var (
	ErrForbidden    = errors.New("catalog operation forbidden")
	ErrInvalidInput = errors.New("invalid catalog input")
	ErrUnavailable  = errors.New("catalog entry unavailable")
)

type ConnectorType string

const (
	ConnectorHTTP       ConnectorType = "HTTP"
	ConnectorPostgreSQL ConnectorType = "POSTGRESQL"
)

type CredentialType string

const (
	CredentialAPIKey            CredentialType = "API_KEY"
	CredentialAccessToken       CredentialType = "ACCESS_TOKEN"
	CredentialUsernamePassword  CredentialType = "USERNAME_PASSWORD"
	CredentialCertificate       CredentialType = "CERTIFICATE"
	CredentialTransitKey        CredentialType = "TRANSIT_KEY"
	CredentialPostgreSQLDynamic CredentialType = "POSTGRESQL_DYNAMIC"
)

type ConnectionConfig struct {
	BaseURL            string
	AllowedHTTPMethods []string
	Host               string
	Port               uint16
	Database           string
	TLSMode            string
	RequireDynamic     bool
}

type Target struct {
	ID                  string
	Name                string
	Description         string
	ConnectorType       ConnectorType
	ConnectionConfig    ConnectionConfig
	DefaultCredentialID string
	Active              bool
	CreatedAt           time.Time
}

type Credential struct {
	ID            string
	Alias         string
	Type          CredentialType
	TargetID      string
	Active        bool
	VaultProvider string
	VaultPath     string
	VaultVersion  *int
	CreatedAt     time.Time
}

type CreateInput struct {
	Name             string
	Description      string
	ConnectorType    ConnectorType
	ConnectionConfig ConnectionConfig
	CredentialAlias  string
	CredentialType   CredentialType
	VaultPath        string
	VaultVersion     *int
}

type Repository interface {
	CreateTargetWithDefaultCredential(context.Context, Target, Credential) error
	ListActiveTargets(context.Context) ([]Target, error)
	FindActiveTargetAndDefaultCredential(context.Context, string) (Target, Credential, error)
}

type Service struct {
	repository Repository
	now        func() time.Time
	newID      func() (string, error)
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, now: time.Now, newID: randomID}
}

func (service *Service) CreateTarget(ctx context.Context, actor identity.User, input CreateInput) (Target, Credential, error) {
	if !actor.CanManageUsersAndTargets() {
		return Target{}, Credential{}, ErrForbidden
	}
	input.Name, input.CredentialAlias, input.VaultPath = strings.TrimSpace(input.Name), strings.TrimSpace(input.CredentialAlias), strings.TrimSpace(input.VaultPath)
	if input.Name == "" || input.CredentialAlias == "" || input.VaultPath == "" || !validCredentialType(input.CredentialType) {
		return Target{}, Credential{}, ErrInvalidInput
	}
	if err := validateConnection(input.ConnectorType, input.ConnectionConfig, input.CredentialType); err != nil {
		return Target{}, Credential{}, err
	}
	targetID, err := service.newID()
	if err != nil {
		return Target{}, Credential{}, fmt.Errorf("generate target ID: %w", err)
	}
	credentialID, err := service.newID()
	if err != nil {
		return Target{}, Credential{}, fmt.Errorf("generate credential ID: %w", err)
	}
	now := service.now()
	target := Target{
		ID: targetID, Name: input.Name, Description: input.Description,
		ConnectorType: input.ConnectorType, ConnectionConfig: input.ConnectionConfig,
		DefaultCredentialID: credentialID, Active: true, CreatedAt: now,
	}
	credential := Credential{
		ID: credentialID, Alias: input.CredentialAlias, Type: input.CredentialType,
		TargetID: targetID, Active: true, VaultProvider: "OPENBAO",
		VaultPath: input.VaultPath, VaultVersion: input.VaultVersion, CreatedAt: now,
	}
	if err := service.repository.CreateTargetWithDefaultCredential(ctx, target, credential); err != nil {
		return Target{}, Credential{}, fmt.Errorf("create target catalog entry: %w", err)
	}
	return target, credential, nil
}

func (service *Service) Discover(ctx context.Context, authenticatedAgentID string) ([]Target, error) {
	if authenticatedAgentID == "" {
		return nil, ErrForbidden
	}
	targets, err := service.repository.ListActiveTargets(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active targets: %w", err)
	}
	return targets, nil
}

// ResolveForRequest chooses the credential from server-owned target metadata.
// No credential identifier is accepted from the Agent.
func (service *Service) ResolveForRequest(ctx context.Context, targetID string) (Target, Credential, error) {
	if targetID == "" {
		return Target{}, Credential{}, ErrInvalidInput
	}
	target, credential, err := service.repository.FindActiveTargetAndDefaultCredential(ctx, targetID)
	if err != nil || !target.Active || !credential.Active || target.DefaultCredentialID != credential.ID || credential.TargetID != target.ID {
		return Target{}, Credential{}, ErrUnavailable
	}
	return target, credential, nil
}

func validateConnection(connector ConnectorType, config ConnectionConfig, credentialType CredentialType) error {
	switch connector {
	case ConnectorHTTP:
		if !slices.Contains([]CredentialType{CredentialAPIKey, CredentialAccessToken, CredentialUsernamePassword, CredentialTransitKey}, credentialType) {
			return ErrInvalidInput
		}
		parsed, err := url.ParseRequestURI(config.BaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return ErrInvalidInput
		}
		if config.Host != "" || config.Port != 0 || config.Database != "" || config.TLSMode != "" || config.RequireDynamic {
			return ErrInvalidInput
		}
		if len(config.AllowedHTTPMethods) == 0 {
			return ErrInvalidInput
		}
		for index, method := range config.AllowedHTTPMethods {
			method = strings.ToUpper(strings.TrimSpace(method))
			if !slices.Contains([]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, method) {
				return ErrInvalidInput
			}
			config.AllowedHTTPMethods[index] = method
		}
	case ConnectorPostgreSQL:
		if credentialType != CredentialUsernamePassword && credentialType != CredentialPostgreSQLDynamic {
			return ErrInvalidInput
		}
		if config.BaseURL != "" || len(config.AllowedHTTPMethods) != 0 || net.ParseIP(config.Host) == nil && !validHostname(config.Host) || config.Port == 0 || strings.TrimSpace(config.Database) == "" {
			return ErrInvalidInput
		}
		if !slices.Contains([]string{"disable", "require", "verify-ca", "verify-full"}, config.TLSMode) {
			return ErrInvalidInput
		}
		if config.RequireDynamic && credentialType != CredentialPostgreSQLDynamic {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	return nil
}

func validCredentialType(value CredentialType) bool {
	return slices.Contains([]CredentialType{
		CredentialAPIKey, CredentialAccessToken, CredentialUsernamePassword,
		CredentialCertificate, CredentialTransitKey, CredentialPostgreSQLDynamic,
	}, value)
}

func validHostname(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
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
