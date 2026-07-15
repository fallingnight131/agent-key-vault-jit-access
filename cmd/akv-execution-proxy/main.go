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
	"github.com/fallingnight/akv/internal/proxy"
	"github.com/fallingnight/akv/internal/store"
	"github.com/fallingnight/akv/internal/vault"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	config, err := proxy.ServerConfigFromEnv()
	if err != nil {
		logger.Error("invalid execution proxy configuration", "error", err)
		os.Exit(2)
	}
	dsnBytes, err := proxy.ReadProtectedConfigFile(config.DatabaseDSNFile)
	if err != nil {
		logger.Error("invalid database configuration", "error", err)
		os.Exit(2)
	}
	database, err := sql.Open("pgx", string(dsnBytes))
	for index := range dsnBytes {
		dsnBytes[index] = 0
	}
	if err != nil {
		logger.Error("invalid database configuration")
		os.Exit(2)
	}
	defer database.Close()
	if err := database.PingContext(context.Background()); err != nil {
		logger.Error("database unavailable")
		os.Exit(1)
	}
	if err := store.Migrate(context.Background(), store.NewPostgreSQLMigrationStore(database)); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	vaultClient, err := vault.NewOpenBaoExecutionClient(config.OpenBaoAddress, config.OpenBaoTokenFile)
	if err != nil {
		logger.Error("invalid OpenBao execution configuration", "error", err)
		os.Exit(2)
	}
	defer vaultClient.Close()
	authorizationRepository := store.NewPostgreSQLAuthorizationRepository(database)
	executionRepository := store.NewPostgreSQLExecutionRepository(database)
	guard := authorization.NewExecutionGuard(authorizationRepository)
	httpProxy := proxy.NewHTTPProxy(executionRepository, guard, vaultClient, executionRepository)
	postgresqlProxy := proxy.NewPostgreSQLProxy(executionRepository, guard, vaultClient, executionRepository, proxy.PGXConnectionFactory{})
	signProxy := proxy.NewSignProxy(executionRepository, guard, vaultClient, executionRepository)
	cancellations := proxy.NewCancellationRegistry()
	httpProxy.SetCancellationRegistry(cancellations)
	postgresqlProxy.SetCancellationRegistry(cancellations)
	signProxy.SetCancellationRegistry(cancellations)
	runtime := &proxy.Runtime{
		Authenticator: agent.NewService(store.NewPostgreSQLAgentRepository(database)),
		Plans:         executionRepository,
		HTTP:          httpProxy,
		PostgreSQL:    postgresqlProxy,
		Sign:          signProxy,
	}
	server := proxy.NewRuntimeServer(config, logger, runtime)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go cancellations.Poll(ctx, executionRepository, time.Second)
	errCh := make(chan error, 1)
	go func() {
		logger.Info("execution proxy listening", "address", config.ListenAddress)
		errCh <- server.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("execution proxy failed", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("execution proxy shutdown failed", "error", err)
			os.Exit(1)
		}
	}
}
