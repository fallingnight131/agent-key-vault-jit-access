package main

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fallingnight/akv/internal/lifecycle"
	"github.com/fallingnight/akv/internal/proxy"
	"github.com/fallingnight/akv/internal/store"
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
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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
		}
	}
}
