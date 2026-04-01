package persistence

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"poolwatch/internal/config"
	"poolwatch/internal/domain"
)

type MongoStore struct {
	client           *mongo.Client
	snapshots        *mongo.Collection
	alerts           *mongo.Collection
	retentionSeconds int32
}

func NewMongo(ctx context.Context, cfg config.Config) (*MongoStore, error) {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoDBURI))
	if err != nil {
		return nil, fmt.Errorf("connect mongodb: %w", err)
	}

	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("ping mongodb: %w", err)
	}

	db := client.Database(cfg.MongoDBDatabase)
	store := &MongoStore{
		client:           client,
		snapshots:        db.Collection(cfg.MongoSnapshotsColl),
		alerts:           db.Collection(cfg.MongoAlertsColl),
		retentionSeconds: int32(cfg.MongoRetention.Seconds()),
	}

	if err := store.ensureIndexes(ctx); err != nil {
		_ = client.Disconnect(ctx)
		return nil, err
	}

	return store, nil
}

func (m *MongoStore) ensureIndexes(ctx context.Context) error {
	snapshotModels := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "timestamp", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(m.retentionSeconds),
		},
		{
			Keys:    bson.D{{Key: "timestamp", Value: -1}},
			Options: options.Index().SetName("snapshot_timestamp_desc"),
		},
	}
	alertModels := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "timestamp", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(m.retentionSeconds),
		},
		{
			Keys:    bson.D{{Key: "timestamp", Value: -1}},
			Options: options.Index().SetName("alert_timestamp_desc"),
		},
	}

	if _, err := m.snapshots.Indexes().CreateMany(ctx, snapshotModels); err != nil {
		return fmt.Errorf("create snapshot indexes: %w", err)
	}
	if _, err := m.alerts.Indexes().CreateMany(ctx, alertModels); err != nil {
		return fmt.Errorf("create alert indexes: %w", err)
	}
	return nil
}

func (m *MongoStore) SaveSnapshot(ctx context.Context, snapshot domain.Snapshot) error {
	_, err := m.snapshots.InsertOne(ctx, snapshot)
	if err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}
	return nil
}

func (m *MongoStore) SaveAlerts(ctx context.Context, alerts []domain.Alert) error {
	if len(alerts) == 0 {
		return nil
	}

	docs := make([]any, 0, len(alerts))
	for _, alert := range alerts {
		docs = append(docs, alert)
	}

	_, err := m.alerts.InsertMany(ctx, docs)
	if err != nil {
		return fmt.Errorf("insert alerts: %w", err)
	}
	return nil
}

func (m *MongoStore) ListSnapshots(ctx context.Context, query HistoryQuery) ([]domain.Snapshot, error) {
	cursor, err := m.snapshots.Find(ctx, timeFilter(query.Window), options.Find().
		SetSort(bson.D{{Key: "timestamp", Value: 1}}).
		SetLimit(normalizeLimit(query.Limit, 300)))
	if err != nil {
		return nil, fmt.Errorf("find snapshots: %w", err)
	}
	defer cursor.Close(ctx)

	var snapshots []domain.Snapshot
	if err := cursor.All(ctx, &snapshots); err != nil {
		return nil, fmt.Errorf("decode snapshots: %w", err)
	}
	return snapshots, nil
}

func (m *MongoStore) ListAlerts(ctx context.Context, query HistoryQuery) ([]domain.Alert, error) {
	cursor, err := m.alerts.Find(ctx, timeFilter(query.Window), options.Find().
		SetSort(bson.D{{Key: "timestamp", Value: -1}}).
		SetLimit(normalizeLimit(query.Limit, 100)))
	if err != nil {
		return nil, fmt.Errorf("find alerts: %w", err)
	}
	defer cursor.Close(ctx)

	var alerts []domain.Alert
	if err := cursor.All(ctx, &alerts); err != nil {
		return nil, fmt.Errorf("decode alerts: %w", err)
	}
	return alerts, nil
}

func (m *MongoStore) ListTimeSeries(ctx context.Context, query HistoryQuery) ([]TimePoint, error) {
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: timeFilter(query.Window)}},
		{{Key: "$sort", Value: bson.D{{Key: "timestamp", Value: 1}}}},
		{{Key: "$limit", Value: normalizeLimit(query.Limit, 300)}},
		{{Key: "$project", Value: bson.D{
			{Key: "_id", Value: 0},
			{Key: "timestamp", Value: "$timestamp"},
			{Key: "pool_usage", Value: "$derived.pool_usage"},
			{Key: "wait_p95_ms", Value: bson.D{{Key: "$divide", Value: bson.A{"$derived.wait_p95", 1000000}}}},
			{Key: "waiting_clients", Value: bson.D{{Key: "$add", Value: bson.A{"$pgbouncer.waiting_clients", "$postgres.waiting_connections"}}}},
			{Key: "active_connections", Value: bson.D{{Key: "$add", Value: bson.A{"$postgres.active_connections", "$pgbouncer.active_servers"}}}},
			{Key: "queue_growth_per_second", Value: "$derived.queue_growth_per_second"},
			{Key: "proxy_healthy", Value: "$proxy.healthy"},
		}}},
	}

	cursor, err := m.snapshots.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("aggregate series: %w", err)
	}
	defer cursor.Close(ctx)

	var points []TimePoint
	if err := cursor.All(ctx, &points); err != nil {
		return nil, fmt.Errorf("decode series: %w", err)
	}
	return points, nil
}

func (m *MongoStore) Close(ctx context.Context) error {
	return m.client.Disconnect(ctx)
}

func timeFilter(window time.Duration) bson.D {
	if window <= 0 {
		return bson.D{}
	}
	return bson.D{{Key: "timestamp", Value: bson.D{{Key: "$gte", Value: time.Now().UTC().Add(-window)}}}}
}

func normalizeLimit(limit, fallback int64) int64 {
	if limit <= 0 {
		return fallback
	}
	if limit > 2000 {
		return 2000
	}
	return limit
}
