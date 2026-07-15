package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fallingnight/akv/internal/agent"
	"github.com/fallingnight/akv/internal/authorization"
	"github.com/fallingnight/akv/internal/catalog"
	"github.com/fallingnight/akv/internal/control"
	"github.com/fallingnight/akv/internal/identity"
	"github.com/fallingnight/akv/internal/lifecycle"
	"github.com/fallingnight/akv/internal/proxy"
	"github.com/fallingnight/akv/internal/store"
	"github.com/fallingnight/akv/internal/task"
	"github.com/fallingnight/akv/internal/vault"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	config, err := control.ConfigFromEnv()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(2)
	}

	dsn, err := proxy.ReadProtectedConfigFile(os.Getenv("AKV_DATABASE_DSN_FILE"))
	if err != nil {
		logger.Error("invalid database configuration", "error", err)
		os.Exit(2)
	}
	database, err := sql.Open("pgx", string(dsn))
	for index := range dsn {
		dsn[index] = 0
	}
	if err != nil {
		logger.Error("database unavailable")
		os.Exit(1)
	}
	pingContext, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPing()
	if err := database.PingContext(pingContext); err != nil {
		logger.Error("database unavailable")
		os.Exit(1)
	}
	defer database.Close()
	if err := store.Migrate(context.Background(), store.NewPostgreSQLMigrationStore(database)); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	controlWriter, err := vault.NewOpenBaoControlClient(os.Getenv("AKV_OPENBAO_CONTROL_ADDRESS"), os.Getenv("AKV_OPENBAO_CONTROL_TOKEN_FILE"))
	if err != nil {
		logger.Error("OpenBao control writer unavailable")
		os.Exit(1)
	}
	defer controlWriter.Close()
	agentService := agent.NewService(store.NewPostgreSQLAgentRepository(database))
	catalogService := catalog.NewManagementService(store.NewPostgreSQLCatalogRepository(database), controlWriter)
	taskService := task.NewService(store.NewPostgreSQLTaskRepository(database))
	requestRepository := store.NewPostgreSQLRequestRepository(database)
	runtime := &control.AgentRuntime{
		Authenticator: agentService, Targets: catalogService, Tasks: taskService,
		Authorizations: authorization.NewService(taskService, catalogService, requestRepository),
		Statuses:       requestRepository,
	}
	identityService, err := identity.NewService(store.NewPostgreSQLIdentityRepository(database))
	if err != nil {
		logger.Error("identity service initialization failed")
		os.Exit(1)
	}
	authorizationRepository := store.NewPostgreSQLAuthorizationRepository(database)
	lifecycleService := lifecycle.NewService(store.NewPostgreSQLLifecycleRepository(database))
	runtime.Revocations = lifecycleService
	webRuntime := &control.WebRuntime{
		Identity: identityService, Agents: agentService, Users: identityService, Catalog: catalogService,
		ApprovalReader: store.NewPostgreSQLWebRepository(database),
		Approvals:      authorization.NewApprovalService(authorizationRepository),
		Revocations:    lifecycleService,
	}
	server := control.NewServer(config, logger, runtime, webRuntime)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("control service listening", "address", config.ListenAddress)
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("control service failed", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("control service shutdown failed", "error", err)
			os.Exit(1)
		}
	}
}
