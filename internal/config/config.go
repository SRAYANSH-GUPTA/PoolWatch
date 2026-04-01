package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Mode string

const (
	ModePassive  Mode = "passive"
	ModeAssisted Mode = "assisted"
	ModeProxy    Mode = "proxy"
)

type Config struct {
	Mode                 Mode
	HTTPAddr             string
	ProxyListenAddr      string
	ProxyUpstreamAddr    string
	PostgresDSN          string
	PgBouncerDSN         string
	MongoDBURI           string
	MongoDBDatabase      string
	MongoSnapshotsColl   string
	MongoAlertsColl      string
	MongoRetention       time.Duration
	CollectInterval      time.Duration
	AnalyzeInterval      time.Duration
	EventQueueSize       int
	HistoryLimit         int
	PoolUsageThreshold   float64
	WaitP95Threshold     time.Duration
	LongQueryThreshold   time.Duration
	LeakThreshold        time.Duration
	ProxyLatencyLimit    time.Duration
	ProxyQueueLimit      int
	ProxyConnectionLimit int
	LogLevelValue        string
}

func Load() (Config, error) {
	cfg := Config{
		Mode:                 Mode(env("POOLWATCH_MODE", string(ModePassive))),
		HTTPAddr:             env("POOLWATCH_HTTP_ADDR", ":8080"),
		ProxyListenAddr:      env("POOLWATCH_PROXY_LISTEN_ADDR", ":6433"),
		ProxyUpstreamAddr:    env("POOLWATCH_PROXY_UPSTREAM_ADDR", "127.0.0.1:6432"),
		PostgresDSN:          os.Getenv("POOLWATCH_POSTGRES_DSN"),
		PgBouncerDSN:         os.Getenv("POOLWATCH_PGBOUNCER_DSN"),
		MongoDBURI:           os.Getenv("POOLWATCH_MONGODB_URI"),
		MongoDBDatabase:      env("POOLWATCH_MONGODB_DATABASE", "poolwatch"),
		MongoSnapshotsColl:   env("POOLWATCH_MONGODB_SNAPSHOTS_COLLECTION", "snapshots"),
		MongoAlertsColl:      env("POOLWATCH_MONGODB_ALERTS_COLLECTION", "alerts"),
		MongoRetention:       durationEnv("POOLWATCH_MONGODB_RETENTION", 7*24*time.Hour),
		CollectInterval:      durationEnv("POOLWATCH_COLLECT_INTERVAL", 2*time.Second),
		AnalyzeInterval:      durationEnv("POOLWATCH_ANALYZE_INTERVAL", 5*time.Second),
		EventQueueSize:       intEnv("POOLWATCH_EVENT_QUEUE_SIZE", 2048),
		HistoryLimit:         intEnv("POOLWATCH_HISTORY_LIMIT", 180),
		PoolUsageThreshold:   floatEnv("POOLWATCH_POOL_USAGE_THRESHOLD", 0.85),
		WaitP95Threshold:     durationEnv("POOLWATCH_WAIT_P95_THRESHOLD", 250*time.Millisecond),
		LongQueryThreshold:   durationEnv("POOLWATCH_LONG_QUERY_THRESHOLD", 5*time.Second),
		LeakThreshold:        durationEnv("POOLWATCH_LEAK_THRESHOLD", 30*time.Second),
		ProxyLatencyLimit:    durationEnv("POOLWATCH_PROXY_LATENCY_LIMIT", time.Millisecond),
		ProxyQueueLimit:      intEnv("POOLWATCH_PROXY_QUEUE_LIMIT", 4096),
		ProxyConnectionLimit: intEnv("POOLWATCH_PROXY_CONNECTION_LIMIT", 10000),
		LogLevelValue:        strings.ToLower(env("POOLWATCH_LOG_LEVEL", "info")),
	}

	switch cfg.Mode {
	case ModePassive, ModeAssisted, ModeProxy:
	default:
		return Config{}, fmt.Errorf("invalid mode %q", cfg.Mode)
	}

	if cfg.EventQueueSize < 1 {
		return Config{}, fmt.Errorf("event queue size must be positive")
	}

	if cfg.HistoryLimit < 10 {
		return Config{}, fmt.Errorf("history limit must be at least 10")
	}

	if cfg.MongoDBURI != "" && cfg.MongoRetention < time.Hour {
		return Config{}, fmt.Errorf("mongodb retention must be at least 1h")
	}

	return cfg, nil
}

func (c Config) LogLevel() slog.Level {
	switch c.LogLevelValue {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func intEnv(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func floatEnv(key string, fallback float64) float64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
