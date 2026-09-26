package analyzer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"poolwatch/internal/config"
	"poolwatch/internal/domain"
	"poolwatch/internal/events"
	"poolwatch/internal/notify"
	"poolwatch/internal/persistence"
	"poolwatch/internal/store"
)

type Service struct {
	cfg      config.Config
	logger   *slog.Logger
	queue    *events.Queue
	state    *store.State
	history  persistence.Store
	notifier notify.Notifier
	dedup    *deduper
}

func New(cfg config.Config, logger *slog.Logger, queue *events.Queue, state *store.State, history persistence.Store, notifier notify.Notifier) *Service {
	if notifier == nil {
		notifier = notify.Noop{}
	}
	return &Service{
		cfg:      cfg,
		logger:   logger,
		queue:    queue,
		state:    state,
		history:  history,
		notifier: notifier,
		dedup:    newDeduper(cfg.AlertCooldown),
	}
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
			alerts := s.dedup.filter(s.analyzeSnapshot(*event.Snapshot), time.Now())
			if len(alerts) == 0 {
				continue
			}
			for _, alert := range alerts {
				s.state.AddAlert(alert, s.cfg.HistoryLimit)
				s.logger.Warn("alert", "code", alert.Code, "message", alert.Message, "severity", alert.Severity)
			}
			if err := s.history.SaveAlerts(ctx, alerts); err != nil {
				s.logger.Warn("persist alerts", "error", err)
			}
			s.notifier.Notify(ctx, alerts)
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

	alerts = append(alerts, s.trendAlerts(snapshot)...)

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

	if snapshot.Mongo.PoolUsage >= s.cfg.PoolUsageThreshold && snapshot.Mongo.CurrentConnections > 0 {
		alerts = append(alerts, domain.Alert{
			Timestamp: snapshot.Timestamp,
			Severity:  domain.SeverityWarn,
			Code:      "mongo_pool_usage_high",
			Message:   fmt.Sprintf("mongo connection usage at %.0f%%", snapshot.Mongo.PoolUsage*100),
			Details: map[string]any{
				"current":   snapshot.Mongo.CurrentConnections,
				"available": snapshot.Mongo.AvailableConnections,
				"active":    snapshot.Mongo.ActiveConnections,
			},
		})
	}

	if snapshot.Mongo.CheckOutFailures > 0 {
		alerts = append(alerts, domain.Alert{
			Timestamp: snapshot.Timestamp,
			Severity:  domain.SeverityError,
			Code:      "mongo_checkout_failures",
			Message:   fmt.Sprintf("%d mongo connection checkouts failed", snapshot.Mongo.CheckOutFailures),
			Details: map[string]any{
				"failures":          snapshot.Mongo.CheckOutFailures,
				"max_checkout_wait": snapshot.Mongo.MaxCheckoutWait.String(),
				"checked_out":       snapshot.Mongo.CheckedOut,
			},
		})
	}

	if len(snapshot.Mongo.SlowOps) > 0 {
		op := snapshot.Mongo.SlowOps[0]
		alerts = append(alerts, domain.Alert{
			Timestamp: snapshot.Timestamp,
			Severity:  domain.SeverityWarn,
			Code:      "mongo_slow_operation",
			Message:   fmt.Sprintf("mongo %s on %s running for %s", op.Op, op.Namespace, op.Duration.Round(time.Millisecond)),
			Details: map[string]any{
				"op_id":     op.OpID,
				"namespace": op.Namespace,
				"client":    op.Client,
				"desc":      op.Desc,
				"slow_ops":  len(snapshot.Mongo.SlowOps),
			},
		})
	}

	if pg := snapshot.Postgres; pg.MaxConnections > 0 && len(pg.AppConnections) > 0 {
		top := pg.AppConnections[0]
		for _, app := range pg.AppConnections[1:] {
			if app.Share > top.Share {
				top = app
			}
		}
		if top.Share >= 0.5 {
			alerts = append(alerts, domain.Alert{
				Timestamp: snapshot.Timestamp,
				Severity:  domain.SeverityWarn,
				Code:      "app_connection_hog",
				Message:   fmt.Sprintf("application %s holds %.0f%% of postgres max_connections on %s", top.Application, top.Share*100, top.Database),
				Details: map[string]any{
					"application": top.Application,
					"database":    top.Database,
					"connections": top.Total,
					"share":       top.Share,
				},
			})
		}
	}

	return alerts
}
