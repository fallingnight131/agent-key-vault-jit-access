package behavior_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/agent"
	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/control"
	"github.com/fallingnight/akv/internal/identity"
	"github.com/fallingnight/akv/internal/lifecycle"
	"github.com/fallingnight/akv/internal/observation"
	"github.com/fallingnight/akv/internal/proxy"
	"github.com/fallingnight/akv/internal/store"
	"github.com/fallingnight/akv/internal/task"
	"github.com/fallingnight/akv/internal/vault"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	behaviorOrigin   = "http://behavior.test"
	sessionCookie    = "akv_session"
	csrfCookie       = "akv_csrf"
	maxBehaviorReply = 2 << 20
)

type lockedBuffer struct {
	mutex sync.Mutex
	data  bytes.Buffer
}

func (buffer *lockedBuffer) Write(value []byte) (int, error) {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return buffer.data.Write(value)
}

func (buffer *lockedBuffer) Contains(value string) bool {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return value != "" && bytes.Contains(buffer.data.Bytes(), []byte(value))
}

type memoryVault struct {
	reads atomic.Int32
	value []byte
}

func (client *memoryVault) ReadKV(context.Context, string, *int) (map[string]*vault.SensitiveValue, error) {
	client.reads.Add(1)
	value := append([]byte(nil), client.value...)
	return map[string]*vault.SensitiveValue{"api_key": vault.NewSensitiveValue(value)}, nil
}

func (*memoryVault) Sign(context.Context, string, string, []byte) ([]byte, error) {
	return nil, errors.New("signing is not part of the behavior fixture")
}

func (*memoryVault) IssueDatabase(context.Context, string, time.Duration) (vault.DynamicCredential, error) {
	return vault.DynamicCredential{}, errors.New("database issuance is not part of the behavior fixture")
}

func (*memoryVault) RevokeLease(context.Context, string) error { return nil }

type behaviorHarness struct {
	database          *sql.DB
	control           *httptest.Server
	execution         *httptest.Server
	target            *httptest.Server
	logs              *lockedBuffer
	vault             *memoryVault
	targetCalls       atomic.Int32
	invalidTargetCall atomic.Int32
	adminPassword     string
	protectedValue    string
	vaultPath         string
	targetRecord      catalog.Target
	operationRecord   catalog.SafeOperation
	operationVersion  catalog.OperationVersion
}

func newBehaviorHarness(t *testing.T) *behaviorHarness {
	t.Helper()
	database := openMarkedBehaviorDatabase(t)
	if _, err := database.ExecContext(context.Background(), `TRUNCATE targets, users CASCADE`); err != nil {
		t.Fatal("reset marked behavior database")
	}

	harness := &behaviorHarness{
		database:       database,
		logs:           &lockedBuffer{},
		adminPassword:  runtimeValue(t, "admin-password"),
		protectedValue: runtimeValue(t, "protected-value"),
		vaultPath:      "kv/data/behavior-fixture",
	}
	harness.vault = &memoryVault{value: []byte(harness.protectedValue)}

	harness.target = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		harness.targetCalls.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/execute" || request.Header.Get("X-API-Key") != harness.protectedValue {
			harness.invalidTargetCall.Add(1)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		response.Header().Set("X-Behavior-Reflected", harness.protectedValue)
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte("accepted " + harness.protectedValue))
	}))
	t.Cleanup(harness.target.Close)

	identityService, err := identity.NewService(store.NewPostgreSQLIdentityRepository(database))
	if err != nil {
		t.Fatal("initialize behavior identity service")
	}
	admin, err := identityService.BootstrapAdmin(context.Background(), "behavior-admin", harness.adminPassword)
	if err != nil {
		t.Fatal("bootstrap behavior administrator")
	}

	catalogService := catalog.NewService(store.NewPostgreSQLCatalogRepository(database))
	harness.targetRecord, _, err = catalogService.CreateTarget(context.Background(), admin, catalog.CreateInput{
		Name:          "behavior-target",
		Description:   "in-process behavior target",
		ConnectorType: catalog.ConnectorHTTP,
		ConnectionConfig: catalog.ConnectionConfig{
			BaseURL: harness.target.URL, AllowedHTTPMethods: []string{http.MethodPost},
		},
		CredentialAlias: "behavior-default", CredentialType: catalog.CredentialAPIKey,
		VaultPath: harness.vaultPath,
	})
	if err != nil {
		t.Fatal("create behavior target")
	}
	operationSet, err := catalogService.CreateOperationSet(context.Background(), admin, catalog.CreateOperationSetInput{
		Name: "behavior-http", Description: "behavior test operations", ExecutorType: catalog.ExecutorHTTP,
	})
	if err != nil {
		t.Fatal("create behavior operation set")
	}
	harness.operationRecord, harness.operationVersion, err = catalogService.CreateOperation(
		context.Background(), admin, operationSet.ID, catalog.PublishOperationInput{
			Key: "execute_behavior", Name: "Execute behavior target", Description: "one protected HTTP operation",
			RiskLevel:         catalog.RiskMedium,
			ArgumentsSchema:   []byte(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
			ExecutionTemplate: []byte(`{"kind":"HTTP","http":{"method":"POST","path":"/execute"}}`),
		},
	)
	if err != nil {
		t.Fatal("create behavior operation")
	}
	if _, err := catalogService.BindOperation(
		context.Background(), admin, harness.targetRecord.ID, harness.operationRecord.ID,
		harness.operationVersion.Version, true,
	); err != nil {
		t.Fatal("bind behavior operation")
	}

	agentService := agent.NewService(store.NewPostgreSQLAgentRepository(database))
	taskService := task.NewService(store.NewPostgreSQLTaskRepository(database))
	requestRepository := store.NewPostgreSQLRequestRepository(database)
	authorizationRepository := store.NewPostgreSQLAuthorizationRepository(database)
	lifecycleService := lifecycle.NewService(store.NewPostgreSQLLifecycleRepository(database))
	agentRuntime := &control.AgentRuntime{
		Authenticator: agentService,
		Targets:       catalogService,
		Operations:    catalogService,
		Tasks:         taskService,
		Authorizations: authorization.NewService(
			taskService, catalogService, requestRepository,
		),
		Statuses:    requestRepository,
		Revocations: lifecycleService,
	}
	webRuntime := &control.WebRuntime{
		Identity:       identityService,
		Agents:         agentService,
		ApprovalReader: store.NewPostgreSQLWebRepository(database),
		Approvals:      authorization.NewApprovalService(authorizationRepository),
		Revocations:    lifecycleService,
		Observations: observation.NewService(
			store.NewPostgreSQLObservationRepository(database),
		),
	}
	logger := slog.New(slog.NewJSONHandler(harness.logs, nil))
	controlServer := control.NewServer(control.Config{
		ListenAddress: "127.0.0.1:0", PublicOrigin: behaviorOrigin, CookieSecure: false,
	}, logger, agentRuntime, webRuntime)
	harness.control = httptest.NewServer(controlServer.Handler)
	t.Cleanup(harness.control.Close)

	executionRepository := store.NewPostgreSQLExecutionRepository(database)
	httpProxy := proxy.NewHTTPProxy(
		executionRepository,
		authorization.NewExecutionGuard(authorizationRepository),
		harness.vault,
		executionRepository,
	)
	executionServer := proxy.NewRuntimeServer(proxy.ServerConfig{ListenAddress: "127.0.0.1:0"}, logger, &proxy.Runtime{
		Authenticator: agentService,
		Plans:         executionRepository,
		HTTP:          httpProxy,
	})
	harness.execution = httptest.NewServer(executionServer.Handler)
	t.Cleanup(harness.execution.Close)
	return harness
}

func openMarkedBehaviorDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("AKV_TEST_POSTGRES_DSN")
	socketMarker := os.Getenv("AKV_TEST_POSTGRES_SOCKET")
	if dsn == "" || socketMarker == "" {
		t.Skip("behavior PostgreSQL markers are not set")
	}
	cleanSocket := filepath.Clean(socketMarker)
	if !filepath.IsAbs(cleanSocket) || filepath.Dir(cleanSocket) != "/tmp" ||
		!strings.HasPrefix(filepath.Base(cleanSocket), "akv-postgres-socket.") {
		t.Fatal("refusing unmarked behavior PostgreSQL socket")
	}
	information, err := os.Stat(cleanSocket)
	if err != nil || !information.IsDir() {
		t.Fatal("marked behavior PostgreSQL socket directory is unavailable")
	}

	database, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("open marked behavior PostgreSQL")
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.PingContext(context.Background()); err != nil {
		t.Fatal("ping marked behavior PostgreSQL")
	}
	var databaseName, databaseUser, socketDirectories string
	if err := database.QueryRowContext(context.Background(), `SELECT current_database(), current_user`).Scan(
		&databaseName, &databaseUser,
	); err != nil {
		t.Fatal("inspect marked behavior PostgreSQL identity")
	}
	if err := database.QueryRowContext(context.Background(), `SHOW unix_socket_directories`).Scan(&socketDirectories); err != nil {
		t.Fatal("inspect marked behavior PostgreSQL socket")
	}
	if databaseName != "akvtest" || databaseUser != "akvtest" || filepath.Clean(socketDirectories) != cleanSocket {
		t.Fatal("refusing PostgreSQL without the exact behavior test identity")
	}
	var usersTable, requestsTable, operationsTable, observationCaptureTable, observationEventsTable sql.NullString
	if err := database.QueryRowContext(context.Background(), `
SELECT
    to_regclass('public.users')::text,
    to_regclass('public.authorization_requests')::text,
    to_regclass('public.operation_versions')::text,
    to_regclass('public.request_observation_capture')::text,
    to_regclass('public.request_observation_events')::text`).Scan(
		&usersTable, &requestsTable, &operationsTable, &observationCaptureTable, &observationEventsTable,
	); err != nil {
		t.Fatal("inspect marked behavior PostgreSQL schema")
	}
	if !usersTable.Valid || usersTable.String != "users" ||
		!requestsTable.Valid || requestsTable.String != "authorization_requests" ||
		!operationsTable.Valid || operationsTable.String != "operation_versions" ||
		!observationCaptureTable.Valid || observationCaptureTable.String != "request_observation_capture" ||
		!observationEventsTable.Valid || observationEventsTable.String != "request_observation_events" {
		t.Fatal("marked behavior PostgreSQL schema is incomplete")
	}
	return database
}

func runtimeValue(t *testing.T, prefix string) string {
	t.Helper()
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		t.Fatal("generate runtime-only behavior value")
	}
	return prefix + "-" + base64.RawURLEncoding.EncodeToString(value)
}

type webActor struct {
	client   *http.Client
	baseURL  string
	origin   string
	userID   string
	username string
	password string
	session  string
	csrf     string
}

func (harness *behaviorHarness) registerWebActor(t *testing.T, role string) *webActor {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal("create behavior cookie jar")
	}
	actor := &webActor{
		client: &http.Client{Jar: jar, Timeout: 5 * time.Second}, baseURL: harness.control.URL,
		origin: behaviorOrigin, username: runtimeValue(t, role), password: runtimeValue(t, "password"),
	}
	status, body := actor.call(t, http.MethodPost, "/v1/web/register", map[string]any{
		"username": actor.username, "password": actor.password,
	}, false)
	if status != http.StatusCreated {
		t.Fatalf("register behavior web actor status=%d", status)
	}
	var publicUser struct {
		ID          string `json:"id"`
		IsAdmin     bool   `json:"is_admin"`
		ApproveAll  bool   `json:"approve_all"`
		OwnerActive bool   `json:"owner_active"`
	}
	decodeBehaviorJSON(t, body, &publicUser)
	if publicUser.ID == "" || publicUser.IsAdmin || publicUser.ApproveAll || !publicUser.OwnerActive {
		t.Fatal("registered behavior actor did not remain an active ordinary user")
	}
	actor.userID = publicUser.ID
	actor.session, actor.csrf = actor.cookieValues(t)
	if actor.session == "" || actor.csrf == "" {
		t.Fatal("behavior registration did not establish session and CSRF cookies")
	}
	return actor
}

func (actor *webActor) call(t *testing.T, method, path string, payload any, mutation bool) (int, []byte) {
	t.Helper()
	return actor.callWithHeaders(t, method, path, payload, mutation, nil)
}

func (actor *webActor) callWithHeaders(
	t *testing.T,
	method, path string,
	payload any,
	mutation bool,
	headers http.Header,
) (int, []byte) {
	t.Helper()
	body := marshalBehaviorJSON(t, payload)
	request, err := http.NewRequestWithContext(context.Background(), method, actor.baseURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal("create behavior web request")
	}
	request.Header.Set("Origin", actor.origin)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	if mutation {
		csrf := actor.csrf
		if csrf == "" {
			_, csrf = actor.cookieValues(t)
		}
		request.Header.Set("X-AKV-CSRF", csrf)
	}
	response, err := actor.client.Do(request)
	if err != nil {
		t.Fatal("perform behavior web request")
	}
	defer response.Body.Close()
	return response.StatusCode, readBehaviorBody(t, response.Body)
}

func (actor *webActor) cookieValues(t *testing.T) (string, string) {
	t.Helper()
	parsed, err := url.Parse(actor.baseURL)
	if err != nil {
		t.Fatal("parse behavior web origin")
	}
	var session, csrf string
	for _, cookie := range actor.client.Jar.Cookies(parsed) {
		switch cookie.Name {
		case sessionCookie:
			session = cookie.Value
		case csrfCookie:
			csrf = cookie.Value
		}
	}
	return session, csrf
}

type behaviorAgent struct {
	ID    string `json:"agent_id"`
	Token string `json:"token"`
}

func (actor *webActor) registerAgent(t *testing.T, name string) behaviorAgent {
	t.Helper()
	status, body := actor.call(t, http.MethodPost, "/v1/web/agents", map[string]any{
		"name": name, "token_lifetime": agent.Token24Hours,
	}, true)
	if status != http.StatusCreated {
		t.Fatalf("register behavior agent status=%d", status)
	}
	var credential behaviorAgent
	decodeBehaviorJSON(t, body, &credential)
	if credential.ID == "" || credential.Token == "" {
		t.Fatal("behavior Agent registration returned an incomplete identity")
	}
	return credential
}

func (harness *behaviorHarness) agentCall(
	t *testing.T,
	baseURL, method, path, token string,
	payload any,
) (int, []byte) {
	t.Helper()
	if token == "" {
		t.Fatal("behavior Agent request has no identity")
	}
	body := marshalBehaviorJSON(t, payload)
	request, err := http.NewRequestWithContext(context.Background(), method, baseURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal("create behavior Agent request")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal("perform behavior Agent request")
	}
	defer response.Body.Close()
	return response.StatusCode, readBehaviorBody(t, response.Body)
}

func marshalBehaviorJSON(t *testing.T, value any) []byte {
	t.Helper()
	if value == nil {
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal("encode behavior request")
	}
	return body
}

func readBehaviorBody(t *testing.T, reader io.Reader) []byte {
	t.Helper()
	body, err := io.ReadAll(io.LimitReader(reader, maxBehaviorReply+1))
	if err != nil || len(body) > maxBehaviorReply {
		t.Fatal("read bounded behavior response")
	}
	return body
}

func decodeBehaviorJSON(t *testing.T, body []byte, destination any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(destination); err != nil {
		t.Fatal("decode behavior response")
	}
}

type dataManifest struct {
	Users                 int `json:"users"`
	Agents                int `json:"agents"`
	Tasks                 int `json:"tasks"`
	AuthorizationRequests int `json:"authorization_requests"`
	ObservationCaptures   int `json:"observation_captures"`
	ObservationEvents     int `json:"observation_events"`
	Approvals             int `json:"approvals"`
	OperationGrants       int `json:"operation_grants"`
	Executions            int `json:"executions"`
	Reclaims              int `json:"reclaims"`
}

func (harness *behaviorHarness) dataManifest(t *testing.T) dataManifest {
	t.Helper()
	var manifest dataManifest
	if err := harness.database.QueryRowContext(context.Background(), `
SELECT
    (SELECT count(*) FROM users),
    (SELECT count(*) FROM agents),
    (SELECT count(*) FROM tasks),
    (SELECT count(*) FROM authorization_requests),
    (SELECT count(*) FROM request_observation_capture),
    (SELECT count(*) FROM request_observation_events),
    (SELECT count(*) FROM approvals),
    (SELECT count(*) FROM operation_grants),
    (SELECT count(*) FROM executions),
    (SELECT count(*) FROM reclaims)`).Scan(
		&manifest.Users,
		&manifest.Agents,
		&manifest.Tasks,
		&manifest.AuthorizationRequests,
		&manifest.ObservationCaptures,
		&manifest.ObservationEvents,
		&manifest.Approvals,
		&manifest.OperationGrants,
		&manifest.Executions,
		&manifest.Reclaims,
	); err != nil {
		t.Fatal("read behavior data manifest")
	}
	return manifest
}

func (harness *behaviorHarness) requestToResultDuration(t *testing.T, requestID string) time.Duration {
	t.Helper()
	var requestedAt, resultCompletedAt time.Time
	if err := harness.database.QueryRowContext(context.Background(), `
SELECT request_record.created_at, execution_record.completed_at
FROM authorization_requests request_record
JOIN operation_grants grant_record ON grant_record.request_id = request_record.id
JOIN executions execution_record ON execution_record.grant_id = grant_record.id
WHERE request_record.id = $1`, requestID).Scan(&requestedAt, &resultCompletedAt); err != nil {
		t.Fatal("read request-to-result timestamp boundaries")
	}
	return resultCompletedAt.Sub(requestedAt)
}

func assertNoRuntimeValues(t *testing.T, body []byte, values ...string) {
	t.Helper()
	for _, value := range values {
		if value != "" && bytes.Contains(body, []byte(value)) {
			t.Fatal("behavior response exposed a runtime-only value")
		}
	}
}

func (harness *behaviorHarness) logsContain(values ...string) bool {
	for _, value := range values {
		if harness.logs.Contains(value) {
			return true
		}
	}
	return false
}
