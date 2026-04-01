package analyzer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"poolwatch/internal/config"
	"poolwatch/internal/domain"
	"poolwatch/internal/events"
	"poolwatch/internal/persistence"
	"poolwatch/internal/store"
)

type Service struct {
	cfg     config.Config
	logger  *slog.Logger
	queue   *events.Queue
	state   *store.State
	history persistence.Store
}

func New(cfg config.Config, logger *slog.Logger, queue *events.Queue, state *store.State, history persistence.Store) *Service {
	return &Service{cfg: cfg, logger: logger, queue: queue, state: state, history: history}
}

func (s *Service) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case event := <-s.queue.Read():
			s.state.SetQueue(s.queue.Len(), s.queue.Dropped())
			if event.Snapshot == nil {
				continue
			}
			alerts := s.analyzeSnapshot(*event.Snapshot)
			for _, alert := range alerts {
				s.state.AddAlert(alert, s.cfg.HistoryLimit)
				s.logger.Warn("alert", "code", alert.Code, "message", alert.Message, "severity", alert.Severity)
			}
			if err := s.history.SaveAlerts(ctx, alerts); err != nil {
				s.logger.Warn("persist alerts", "error", err)
			}
		}
	}
}

func (s *Service) analyzeSnapshot(snapshot domain.Snapshot) []domain.Alert {
	var alerts []domain.Alert

	if snapshot.Derived.PoolUsage >= s.cfg.PoolUsageThreshold {
		alerts = append(alerts, domain.Alert{
			Timestamp: snapshot.Timestamp,
			Severity:  domain.SeverityWarn,
			Code:      "pool_usage_high",
			Message:   fmt.Sprintf("pool usage at %.0f%%", snapshot.Derived.PoolUsage*100),
			Details: map[string]any{
				"pool_usage": snapshot.Derived.PoolUsage,
			},
		})
	}

	if snapshot.Derived.WaitP95 >= s.cfg.WaitP95Threshold {
		alerts = append(alerts, domain.Alert{
			Timestamp: snapshot.Timestamp,
			Severity:  domain.SeverityWarn,
			Code:      "wait_time_high",
			Message:   fmt.Sprintf("p95 connection wait at %s", snapshot.Derived.WaitP95),
			Details: map[string]any{
				"wait_p95": snapshot.Derived.WaitP95.String(),
			},
		})
	}

	if snapshot.Derived.ExhaustionSeconds != nil && *snapshot.Derived.ExhaustionSeconds <= 30 {
		alerts = append(alerts, domain.Alert{
			Timestamp: snapshot.Timestamp,
			Severity:  domain.SeverityError,
			Code:      "pool_exhaustion_predicted",
			Message:   fmt.Sprintf("pool may exhaust in %d seconds", *snapshot.Derived.ExhaustionSeconds),
			Details: map[string]any{
				"exhaustion_seconds": *snapshot.Derived.ExhaustionSeconds,
				"queue_growth_rate":  snapshot.Derived.QueueGrowthPerSecond,
			},
		})
	}

	if len(snapshot.Postgres.QueryHogs) > 0 {
		hog := snapshot.Postgres.QueryHogs[0]
		if hog.Duration >= s.cfg.LongQueryThreshold {
			alerts = append(alerts, domain.Alert{
				Timestamp: snapshot.Timestamp,
				Severity:  domain.SeverityWarn,
				Code:      "query_hog_detected",
				Message:   fmt.Sprintf("long-running query from %s holding connection for %s", hog.Application, hog.Duration.Truncate(time.Millisecond)),
				Details: map[string]any{
					"application": hog.Application,
					"database":    hog.Database,
					"duration":    hog.Duration.String(),
					"query":       hog.Query,
				},
			})
		}
	}

	for _, leak := range snapshot.Postgres.LeakCandidates {
		if leak.IdleFor >= s.cfg.LeakThreshold {
			alerts = append(alerts, domain.Alert{
				Timestamp: snapshot.Timestamp,
				Severity:  domain.SeverityWarn,
				Code:      "connection_leak_candidate",
				Message:   fmt.Sprintf("possible leaked connection in %s for %s", leak.Application, leak.IdleFor.Truncate(time.Millisecond)),
				Details: map[string]any{
					"application": leak.Application,
					"database":    leak.Database,
					"idle_for":    leak.IdleFor.String(),
					"in_txn":      leak.InTxn,
				},
			})
			break
		}
	}

	if snapshot.PgBouncer.WaitingClients > 0 && snapshot.Derived.QueueGrowthPerSecond > 0 && snapshot.Derived.PoolUsage >= s.cfg.PoolUsageThreshold {
		origin := "unknown"
		if len(snapshot.Postgres.QueryHogs) > 0 && snapshot.Postgres.QueryHogs[0].Application != "" {
			origin = snapshot.Postgres.QueryHogs[0].Application
		}
		alerts = append(alerts, domain.Alert{
			Timestamp: snapshot.Timestamp,
			Severity:  domain.SeverityError,
			Code:      "root_cause_pool_exhaustion",
			Message:   fmt.Sprintf("pool exhaustion pressure driven by %d long-running queries from %s", len(snapshot.Postgres.QueryHogs), origin),
			Details: map[string]any{
				"waiting_clients": snapshot.PgBouncer.WaitingClients,
				"origin_service":  origin,
				"long_queries":    len(snapshot.Postgres.QueryHogs),
			},
		})
	}

	if snapshot.Proxy.Enabled && !snapshot.Proxy.Healthy {
		alerts = append(alerts, domain.Alert{
			Timestamp: snapshot.Timestamp,
			Severity:  domain.SeverityError,
			Code:      "proxy_unhealthy",
			Message:   "proxy mode auto-disabled due to health guard",
			Details: map[string]any{
				"last_error": snapshot.Proxy.LastError,
			},
		})
	}

	return alerts
}
