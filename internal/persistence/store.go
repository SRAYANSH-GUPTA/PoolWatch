package persistence

import (
	"context"
	"time"

	"poolwatch/internal/domain"
)

type HistoryQuery struct {
	Window time.Duration
	Limit  int64
}

type TimePoint struct {
	Timestamp            time.Time `json:"timestamp" bson:"timestamp"`
	PoolUsage            float64   `json:"pool_usage" bson:"pool_usage"`
	WaitP95MS            float64   `json:"wait_p95_ms" bson:"wait_p95_ms"`
	WaitingClients       int       `json:"waiting_clients" bson:"waiting_clients"`
	ActiveConnections    int       `json:"active_connections" bson:"active_connections"`
	QueueGrowthPerSecond float64   `json:"queue_growth_per_second" bson:"queue_growth_per_second"`
	ProxyHealthy         bool      `json:"proxy_healthy" bson:"proxy_healthy"`
}

type Store interface {
	SaveSnapshot(ctx context.Context, snapshot domain.Snapshot) error
	SaveAlerts(ctx context.Context, alerts []domain.Alert) error
	ListSnapshots(ctx context.Context, query HistoryQuery) ([]domain.Snapshot, error)
	ListAlerts(ctx context.Context, query HistoryQuery) ([]domain.Alert, error)
	ListTimeSeries(ctx context.Context, query HistoryQuery) ([]TimePoint, error)
	Close(ctx context.Context) error
}

type NoopStore struct{}

func NewNoop() Store {
	return NoopStore{}
}

func (NoopStore) SaveSnapshot(context.Context, domain.Snapshot) error {
	return nil
}

func (NoopStore) SaveAlerts(context.Context, []domain.Alert) error {
	return nil
}

func (NoopStore) ListSnapshots(context.Context, HistoryQuery) ([]domain.Snapshot, error) {
	return nil, nil
}

func (NoopStore) ListAlerts(context.Context, HistoryQuery) ([]domain.Alert, error) {
	return nil, nil
}

func (NoopStore) ListTimeSeries(context.Context, HistoryQuery) ([]TimePoint, error) {
	return nil, nil
}

func (NoopStore) Close(context.Context) error {
	return nil
}
