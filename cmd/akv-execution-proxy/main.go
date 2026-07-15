package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fallingnight/akv/internal/proxy"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	config, err := proxy.ServerConfigFromEnv()
	if err != nil {
		logger.Error("invalid execution proxy configuration", "error", err)
		os.Exit(2)
	}
	server := proxy.NewRuntimeServer(config, logger)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
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
