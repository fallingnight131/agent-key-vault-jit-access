package main

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fallingnight/akv/internal/audit"
	"github.com/fallingnight/akv/internal/lifecycle"
	"github.com/fallingnight/akv/internal/proxy"
	"github.com/fallingnight/akv/internal/store"
	"github.com/fallingnight/akv/internal/vault"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	dsnFile := os.Getenv("AKV_DATABASE_DSN_FILE")
	dsn, err := proxy.ReadProtectedConfigFile(dsnFile)
	if err != nil {
		logger.Error("invalid database configuration", "error", err)
		os.Exit(2)
	}
	database, err := sql.Open("pgx", string(dsn))
	for index := range dsn {
		dsn[index] = 0
	}
	if err != nil {
		logger.Error("invalid database configuration")
		os.Exit(2)
	}
	defer database.Close()
	if err := database.Ping(); err != nil {
		logger.Error("database unavailable")
		os.Exit(1)
	}
	if err := store.Migrate(context.Background(), store.NewPostgreSQLMigrationStore(database)); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	service := lifecycle.NewService(store.NewPostgreSQLLifecycleRepository(database))
	auditService := audit.NewService(store.NewPostgreSQLAuditRepository(database))
	vaultClient, err := vault.NewOpenBaoExecutionClient(os.Getenv("AKV_OPENBAO_ADDRESS"), os.Getenv("AKV_OPENBAO_TOKEN_FILE"))
	if err != nil {
		logger.Error("invalid OpenBao recovery configuration", "error", err)
		os.Exit(2)
	}
	defer vaultClient.Close()
	recoveryService := lifecycle.NewRecoveryService(store.NewPostgreSQLExecutionRepository(database), vaultClient)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			recovery, err := recoveryService.Recover(ctx)
			if err != nil {
				logger.Error("execution recovery failed", "error", err)
			} else if recovery.Candidates > 0 {
				logger.Info("execution recovery completed", "recovered", recovery.Recovered, "failed", recovery.Failed)
			}
			result, err := service.Sweep(ctx)
			if err != nil {
				logger.Error("lifecycle sweep failed", "error", err)
				continue
			}
			if result.ExpiredRequests+result.ExpiredGrants+result.LostTasks > 0 || len(result.CancelledExecutions) > 0 {
				logger.Info("lifecycle sweep completed",
					"expired_requests", result.ExpiredRequests,
					"expired_grants", result.ExpiredGrants,
					"lost_tasks", result.LostTasks,
					"cancellation_requests", len(result.CancelledExecutions))
			}
			deleted, err := auditService.Cleanup(ctx)
			if err != nil {
				logger.Error("audit cleanup failed", "error", err)
			} else if deleted > 0 {
				logger.Info("expired audit events deleted", "count", deleted)
			}
		}
	}
}
