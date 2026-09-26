package collector

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"poolwatch/internal/domain"
)

// mongoMonitor wraps a monitoring client and driver-side pool statistics
// gathered from an event.PoolMonitor.
type mongoMonitor struct {
	client *mongo.Client
	pool   *mongoPoolStats
}

// mongoPoolStats tracks driver pool events. CheckedOut is a live gauge;
// failures and wait stats are windowed and reset on every collection.
type mongoPoolStats struct {
	mu         sync.Mutex
	checkedOut int64
	failures   int64
	waitCount  int64
	waitTotal  time.Duration
	waitMax    time.Duration
}

func (p *mongoPoolStats) handle(evt *event.PoolEvent) {
	if evt == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch evt.Type {
	case event.GetSucceeded:
		p.checkedOut++
		p.observeWait(evt.Duration)
	case event.GetFailed:
		p.failures++
		p.observeWait(evt.Duration)
	case event.ConnectionReturned:
		if p.checkedOut > 0 {
			p.checkedOut--
		}
	}
}

func (p *mongoPoolStats) observeWait(d time.Duration) {
	p.waitCount++
	p.waitTotal += d
	if d > p.waitMax {
		p.waitMax = d
	}
}

func (p *mongoPoolStats) drain(result *domain.MongoMetrics) {
	p.mu.Lock()
	defer p.mu.Unlock()
	result.CheckedOut = p.checkedOut
	result.CheckOutFailures = p.failures
	result.MaxCheckoutWait = p.waitMax
	if p.waitCount > 0 {
		result.AvgCheckoutWait = p.waitTotal / time.Duration(p.waitCount)
	}
	p.failures, p.waitCount, p.waitTotal, p.waitMax = 0, 0, 0, 0
}

func newMongoMonitor(ctx context.Context, uri string) (*mongoMonitor, error) {
	stats := &mongoPoolStats{}
	opts := options.Client().
		ApplyURI(uri).
		SetAppName("poolwatch-monitor").
		SetPoolMonitor(&event.PoolMonitor{Event: stats.handle})

	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("connect mongo monitor: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("ping mongo monitor: %w", err)
	}
	return &mongoMonitor{client: client, pool: stats}, nil
}

func (s *Service) collectMongo(ctx context.Context) (domain.MongoMetrics, error) {
	result := domain.MongoMetrics{}
	admin := s.mongo.client.Database("admin")

	var status struct {
		Connections struct {
			Current      int64 `bson:"current"`
			Available    int64 `bson:"available"`
			Active       int64 `bson:"active"`
			TotalCreated int64 `bson:"totalCreated"`
		} `bson:"connections"`
	}
	if err := admin.RunCommand(ctx, bson.D{{Key: "serverStatus", Value: 1}}).Decode(&status); err != nil {
		return result, fmt.Errorf("serverStatus: %w", err)
	}
	result.CurrentConnections = status.Connections.Current
	result.AvailableConnections = status.Connections.Available
	result.ActiveConnections = status.Connections.Active
	result.TotalCreated = status.Connections.TotalCreated
	if total := result.CurrentConnections + result.AvailableConnections; total > 0 {
		result.PoolUsage = float64(result.CurrentConnections) / float64(total)
	}

	s.mongo.pool.drain(&result)

	// currentOp may require privileges; failure leaves SlowOps empty.
	result.SlowOps, _ = s.collectMongoSlowOps(ctx, admin)

	return result, nil
}

func (s *Service) collectMongoSlowOps(ctx context.Context, admin *mongo.Database) ([]domain.MongoSlowOp, error) {
	threshold := s.cfg.LongQueryThreshold
	cmd := bson.D{
		{Key: "currentOp", Value: 1},
		{Key: "active", Value: true},
		{Key: "microsecs_running", Value: bson.D{{Key: "$gte", Value: threshold.Microseconds()}}},
	}

	var reply struct {
		InProg []struct {
			OpID             any    `bson:"opid"`
			Namespace        string `bson:"ns"`
			Op               string `bson:"op"`
			SecsRunning      int64  `bson:"secs_running"`
			MicrosecsRunning int64  `bson:"microsecs_running"`
			Client           string `bson:"client"`
			Desc             string `bson:"desc"`
		} `bson:"inprog"`
	}
	if err := admin.RunCommand(ctx, cmd).Decode(&reply); err != nil {
		return nil, err
	}

	ops := make([]domain.MongoSlowOp, 0, len(reply.InProg))
	for _, op := range reply.InProg {
		duration := time.Duration(op.MicrosecsRunning) * time.Microsecond
		if duration == 0 {
			duration = time.Duration(op.SecsRunning) * time.Second
		}
		if duration < threshold {
			continue
		}
		ops = append(ops, domain.MongoSlowOp{
			OpID:      op.OpID,
			Namespace: op.Namespace,
			Op:        op.Op,
			Duration:  duration,
			Client:    op.Client,
			Desc:      op.Desc,
		})
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Duration > ops[j].Duration })
	return ops, nil
}
