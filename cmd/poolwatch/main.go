package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"poolwatch/internal/config"
	"poolwatch/internal/runtime"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz endpoint and exit 0 if healthy (for Docker HEALTHCHECK)")
	printConfig := flag.Bool("print-config", false, "print the effective configuration as JSON with secrets redacted, then exit")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	if *printConfig {
		os.Exit(dumpConfig(cfg))
	}

	if *healthcheck {
		os.Exit(probe(cfg.HTTPAddr))
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

// probe calls GET /healthz on the configured HTTP address. The distroless image
// has no shell or curl, so the binary doubles as its own health checker.
func probe(addr string) int {
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: status", resp.StatusCode)
		return 1
	}
	return 0
}

var dsnPassword = regexp.MustCompile(`password=\S+`)

// dumpConfig prints the effective configuration so operators can verify env
// wiring without leaking credentials.
func dumpConfig(cfg config.Config) int {
	cfg.PostgresDSN = redact(cfg.PostgresDSN)
	cfg.PgBouncerDSN = redact(cfg.PgBouncerDSN)
	cfg.MongoDBURI = redact(cfg.MongoDBURI)
	cfg.MongoMonitorURI = redact(cfg.MongoMonitorURI)
	if cfg.AlertWebhookURL != "" {
		cfg.AlertWebhookURL = "[redacted]"
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "print-config:", err)
		return 1
	}
	return 0
}

func redact(dsn string) string {
	if dsn == "" {
		return dsn
	}
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		return u.Redacted()
	}
	return dsnPassword.ReplaceAllString(dsn, "password=xxxxx")
}
