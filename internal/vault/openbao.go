package vault

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const maxOpenBaoResponse = 1 << 20

type OpenBaoExecutionClient struct {
	baseURL    *url.URL
	token      *SensitiveValue
	httpClient *http.Client
}

type OpenBaoControlClient struct {
	client *OpenBaoExecutionClient
}

func NewOpenBaoControlClient(address, tokenFile string) (*OpenBaoControlClient, error) {
	client, err := NewOpenBaoExecutionClient(address, tokenFile)
	if err != nil {
		return nil, err
	}
	return &OpenBaoControlClient{client: client}, nil
}

func (client *OpenBaoControlClient) Close() { client.client.Close() }

func (client *OpenBaoControlClient) WriteKV(ctx context.Context, write KVWrite) error {
	if strings.TrimSpace(write.Path) == "" || len(write.Values) == 0 {
		return ErrUnavailable
	}
	values := make(map[string]string, len(write.Values))
	for name, value := range write.Values {
		if strings.TrimSpace(name) == "" || value == nil {
			return ErrUnavailable
		}
		if err := value.WithBytes(func(raw []byte) error {
			values[name] = string(raw)
			return nil
		}); err != nil {
			return ErrUnavailable
		}
	}
	return client.client.call(ctx, http.MethodPost, write.Path, nil, map[string]any{"data": values}, nil)
}

func (client *OpenBaoControlClient) ConfigureTransitKey(ctx context.Context, key TransitKey) error {
	if strings.TrimSpace(key.Name) == "" || !slices.Contains([]string{"rsa-2048", "rsa-3072", "rsa-4096", "ecdsa-p256", "ecdsa-p384", "ecdsa-p521"}, key.Type) {
		return ErrUnavailable
	}
	return client.client.call(ctx, http.MethodPost, "transit/keys/"+url.PathEscape(key.Name), nil, map[string]any{"type": key.Type, "exportable": false, "allow_plaintext_backup": false}, nil)
}

func (client *OpenBaoControlClient) ConfigureDatabaseRole(ctx context.Context, role DatabaseRole) error {
	if strings.TrimSpace(role.Name) == "" || strings.TrimSpace(role.ConnectionName) == "" || len(role.CreationStatements) == 0 || role.DefaultTTL <= 0 || role.MaxTTL < role.DefaultTTL {
		return ErrUnavailable
	}
	path := "database/roles/" + url.PathEscape(role.Name)
	payload := map[string]any{
		"db_name": role.ConnectionName, "creation_statements": role.CreationStatements,
		"default_ttl": role.DefaultTTL.String(), "max_ttl": role.MaxTTL.String(),
	}
	return client.client.call(ctx, http.MethodPost, path, nil, payload, nil)
}

func NewOpenBaoExecutionClient(address, tokenFile string) (*OpenBaoExecutionClient, error) {
	baseURL, err := url.Parse(strings.TrimRight(address, "/"))
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" || baseURL.User != nil {
		return nil, errors.New("invalid OpenBao address")
	}
	token, err := readProtectedToken(tokenFile)
	if err != nil {
		return nil, err
	}
	return &OpenBaoExecutionClient{
		baseURL: baseURL, token: NewSensitiveValue(token),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func readProtectedToken(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat OpenBao token file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("OpenBao token file must be a regular file inaccessible to group and others")
	}
	value, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read OpenBao token file: %w", err)
	}
	value = bytes.TrimSpace(value)
	if len(value) == 0 {
		return nil, errors.New("OpenBao token file is empty")
	}
	return value, nil
}

func (client *OpenBaoExecutionClient) Close() { client.token.Destroy() }

func (client *OpenBaoExecutionClient) ReadKV(ctx context.Context, path string, version *int) (map[string]*SensitiveValue, error) {
	query := url.Values{}
	if version != nil {
		query.Set("version", fmt.Sprint(*version))
	}
	var response struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	if err := client.call(ctx, http.MethodGet, path, query, nil, &response); err != nil {
		return nil, err
	}
	values := make(map[string]*SensitiveValue, len(response.Data.Data))
	for name, value := range response.Data.Data {
		values[name] = NewSensitiveValue([]byte(value))
	}
	return values, nil
}

func (client *OpenBaoExecutionClient) Sign(ctx context.Context, path, algorithm string, digest []byte) ([]byte, error) {
	path = strings.Replace(path, "/keys/", "/sign/", 1)
	payload := map[string]any{
		"input": base64.StdEncoding.EncodeToString(digest), "prehashed": true, "hash_algorithm": algorithm,
	}
	var response struct {
		Data struct {
			Signature string `json:"signature"`
		} `json:"data"`
	}
	if err := client.call(ctx, http.MethodPost, path, nil, payload, &response); err != nil || response.Data.Signature == "" {
		return nil, ErrUnavailable
	}
	return []byte(response.Data.Signature), nil
}

func (client *OpenBaoExecutionClient) IssueDatabase(ctx context.Context, path string, ttl time.Duration) (DynamicCredential, error) {
	query := url.Values{"ttl": []string{ttl.String()}}
	var response struct {
		LeaseID       string `json:"lease_id"`
		LeaseDuration int64  `json:"lease_duration"`
		Data          struct {
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"data"`
	}
	if err := client.call(ctx, http.MethodGet, path, query, nil, &response); err != nil || response.LeaseID == "" || response.Data.Username == "" || response.Data.Password == "" {
		return DynamicCredential{}, ErrUnavailable
	}
	return DynamicCredential{
		Username: NewSensitiveValue([]byte(response.Data.Username)), Password: NewSensitiveValue([]byte(response.Data.Password)),
		LeaseID: response.LeaseID, ExpiresAt: time.Now().Add(time.Duration(response.LeaseDuration) * time.Second),
	}, nil
}

func (client *OpenBaoExecutionClient) RevokeLease(ctx context.Context, leaseID string) error {
	if leaseID == "" {
		return ErrUnavailable
	}
	return client.call(ctx, http.MethodPut, "sys/leases/revoke", nil, map[string]string{"lease_id": leaseID}, nil)
}

func (client *OpenBaoExecutionClient) call(ctx context.Context, method, path string, query url.Values, payload, output any) error {
	endpoint := *client.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/v1/" + strings.TrimLeft(path, "/")
	if query != nil {
		endpoint.RawQuery = query.Encode()
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return ErrUnavailable
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return ErrUnavailable
	}
	request.Header.Set("Content-Type", "application/json")
	if err := client.token.WithBytes(func(token []byte) error {
		request.Header.Set("X-Vault-Token", string(token))
		return nil
	}); err != nil {
		return ErrUnavailable
	}
	response, err := client.httpClient.Do(request)
	request.Header.Del("X-Vault-Token")
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxOpenBaoResponse))
		return ErrUnavailable
	}
	if output == nil {
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxOpenBaoResponse))
	if err := decoder.Decode(output); err != nil {
		return ErrUnavailable
	}
	return nil
}
