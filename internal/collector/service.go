package collector

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"poolwatch/internal/config"
	"poolwatch/internal/domain"
	"poolwatch/internal/events"
	"poolwatch/internal/proxy"
	"poolwatch/internal/store"
)

type Service struct {
	cfg      config.Config
	logger   *slog.Logger
	queue    *events.Queue
	state    *store.State
	proxy    *proxy.Service
	postgres *sql.DB
	pgb      *sql.DB
}

func New(cfg config.Config, logger *slog.Logger, queue *events.Queue, state *store.State, proxyService *proxy.Service) (*Service, error) {
	service := &Service{
		cfg:    cfg,
		logger: logger,
		queue:  queue,
		state:  state,
		proxy:  proxyService,
	}

	if cfg.PostgresDSN != "" {
		db, err := sql.Open("pgx", cfg.PostgresDSN)
		if err != nil {
			return nil, fmt.Errorf("open postgres: %w", err)
		}
		db.SetMaxOpenConns(2)
		db.SetMaxIdleConns(2)
		db.SetConnMaxLifetime(2 * time.Minute)
		service.postgres = db
	}

	if cfg.PgBouncerDSN != "" {
		db, err := sql.Open("pgx", cfg.PgBouncerDSN)
		if err != nil {
			return nil, fmt.Errorf("open pgbouncer: %w", err)
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		db.SetConnMaxLifetime(2 * time.Minute)
		service.pgb = db
	}

	return service, nil
}

func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.cfg.CollectInterval)
	defer ticker.Stop()
	defer s.close()

	if err := s.collectOnce(ctx); err != nil {
		s.logger.Warn("initial collection failed", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := s.collectOnce(ctx); err != nil {
				s.logger.Warn("collection failed", "error", err)
			}
		}
	}
}

func (s *Service) close() {
	if s.postgres != nil {
		_ = s.postgres.Close()
	}
	if s.pgb != nil {
		_ = s.pgb.Close()
	}
}

func (s *Service) collectOnce(ctx context.Context) error {
	snapshot := domain.Snapshot{
		Timestamp:         time.Now().UTC(),
		CollectorErrors:   map[string]string{},
		SourceLatenciesMS: map[string]float64{},
	}

	if s.postgres != nil {
		start := time.Now()
		metrics, err := s.collectPostgres(ctx)
		snapshot.SourceLatenciesMS["postgres"] = float64(time.Since(start).Microseconds()) / 1000
		if err != nil {
			snapshot.CollectorErrors["postgres"] = err.Error()
		} else {
			snapshot.Postgres = metrics
		}
	} else {
		snapshot.CollectorErrors["postgres"] = "postgres dsn not configured"
	}

	if s.pgb != nil {
		start := time.Now()
		metrics, err := s.collectPgBouncer(ctx)
		snapshot.SourceLatenciesMS["pgbouncer"] = float64(time.Since(start).Microseconds()) / 1000
		if err != nil {
			snapshot.CollectorErrors["pgbouncer"] = err.Error()
		} else {
			snapshot.PgBouncer = metrics
		}
	} else {
		snapshot.CollectorErrors["pgbouncer"] = "pgbouncer dsn not configured"
	}

	snapshot.Proxy = s.proxy.Metrics()
	snapshot.Derived = deriveMetrics(s.state.History(), snapshot)

	s.state.UpdateSnapshot(snapshot, s.cfg.HistoryLimit)
	s.state.SetQueue(s.queue.Len(), s.queue.Dropped())
	s.state.SetProxyStatus(snapshot.Proxy.Enabled, snapshot.Proxy.Healthy)
	s.queue.Publish(domain.Event{
		Type:      "snapshot",
		Timestamp: snapshot.Timestamp,
		Snapshot:  &snapshot,
	})

	return nil
}

func deriveMetrics(history []domain.Snapshot, current domain.Snapshot) domain.DerivedMetrics {
	result := domain.DerivedMetrics{}
	if current.Postgres.MaxConnections > 0 {
		result.PoolUsage = float64(current.Postgres.ActiveConnections+current.PgBouncer.ActiveServers) / float64(current.Postgres.MaxConnections)
	} else {
		total := current.PgBouncer.ActiveServers + current.PgBouncer.IdleServers
		if total > 0 {
			result.PoolUsage = float64(current.PgBouncer.ActiveServers) / float64(total)
		}
	}

	samples := append([]time.Duration{}, current.PgBouncer.WaitSamples...)
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	if len(samples) > 0 {
		result.WaitP50 = percentile(samples, 0.50)
		result.WaitP95 = percentile(samples, 0.95)
		result.WaitP99 = percentile(samples, 0.99)
	}

	if len(history) > 0 {
		previous := history[len(history)-1]
		seconds := current.Timestamp.Sub(previous.Timestamp).Seconds()
		if seconds > 0 {
			queueNow := float64(current.PgBouncer.WaitingClients + current.Postgres.WaitingConnections)
			queuePrev := float64(previous.PgBouncer.WaitingClients + previous.Postgres.WaitingConnections)
			connNow := float64(current.Postgres.ActiveConnections + current.PgBouncer.ActiveClients)
			connPrev := float64(previous.Postgres.ActiveConnections + previous.PgBouncer.ActiveClients)
			result.QueueGrowthPerSecond = (queueNow - queuePrev) / seconds
			result.ConnectionGrowthPerSec = (connNow - connPrev) / seconds
			if result.QueueGrowthPerSecond > 0 {
				capacity := float64(current.Postgres.MaxConnections - current.Postgres.ActiveConnections)
				if capacity <= 0 {
					zero := int64(0)
					result.ExhaustionSeconds = &zero
				} else {
					value := int64(capacity / result.QueueGrowthPerSecond)
					if value >= 0 {
						result.ExhaustionSeconds = &value
					}
				}
			}
		}
	}

	return result
}

func percentile(samples []time.Duration, ratio float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	index := int(float64(len(samples)-1) * ratio)
	if index < 0 {
		index = 0
	}
	if index >= len(samples) {
		index = len(samples) - 1
	}
	return samples[index]
}
