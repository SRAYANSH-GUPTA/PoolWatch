package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"poolwatch/internal/config"
	"poolwatch/internal/runtime"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel()}))
	app, err := runtime.New(cfg, logger)
	if err != nil {
		logger.Error("build runtime", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx); err != nil {
		logger.Error("run runtime", "error", err)
		os.Exit(1)
	}
}
