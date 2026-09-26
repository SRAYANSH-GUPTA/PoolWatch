package collector

import (
	"math"
	"testing"
	"time"

	"poolwatch/internal/domain"
)

func TestPercentile(t *testing.T) {
	ms := time.Millisecond
	sorted := []time.Duration{1 * ms, 2 * ms, 3 * ms, 4 * ms, 5 * ms, 6 * ms, 7 * ms, 8 * ms, 9 * ms, 10 * ms}
	tests := []struct {
		name    string
		samples []time.Duration
		ratio   float64
		want    time.Duration
	}{
		{"empty", nil, 0.5, 0},
		{"single", []time.Duration{7 * ms}, 0.99, 7 * ms},
		{"p0", sorted, 0, 1 * ms},
		{"p50", sorted, 0.50, 5 * ms},
		{"p95", sorted, 0.95, 9 * ms},
		{"p100", sorted, 1.0, 10 * ms},
		{"negative clamps", sorted, -1, 1 * ms},
		{"above one clamps", sorted, 2, 10 * ms},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := percentile(tc.samples, tc.ratio); got != tc.want {
				t.Fatalf("percentile(%v) = %v, want %v", tc.ratio, got, tc.want)
			}
		})
	}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestDeriveMetricsPoolUsage(t *testing.T) {
	tests := []struct {
		name string
		snap domain.Snapshot
		want float64
	}{
		{
			name: "max_connections based",
			snap: domain.Snapshot{
				Postgres:  domain.PostgresMetrics{MaxConnections: 100, ActiveConnections: 40},
				PgBouncer: domain.PgBouncerMetrics{ActiveServers: 10, IdleServers: 90},
			},
			want: 0.5,
		},
		{
			name: "pgbouncer fallback",
			snap: domain.Snapshot{
				PgBouncer: domain.PgBouncerMetrics{ActiveServers: 3, IdleServers: 1},
			},
			want: 0.75,
		},
		{
			name: "no data",
			snap: domain.Snapshot{},
			want: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveMetrics(nil, tc.snap)
			if !approx(got.PoolUsage, tc.want) {
				t.Fatalf("PoolUsage = %v, want %v", got.PoolUsage, tc.want)
			}
			if got.ExhaustionSeconds != nil {
				t.Fatalf("expected no exhaustion ETA without history")
			}
		})
	}
}

func TestDeriveMetricsWaitPercentiles(t *testing.T) {
	ms := time.Millisecond
	snap := domain.Snapshot{PgBouncer: domain.PgBouncerMetrics{
		WaitSamples: []time.Duration{50 * ms, 10 * ms, 40 * ms, 20 * ms, 30 * ms},
	}}
	got := deriveMetrics(nil, snap)
	if got.WaitP50 != 30*ms || got.WaitP95 != 40*ms || got.WaitP99 != 40*ms {
		t.Fatalf("unexpected percentiles p50=%v p95=%v p99=%v", got.WaitP50, got.WaitP95, got.WaitP99)
	}
	// input must not be reordered
	if snap.PgBouncer.WaitSamples[0] != 50*ms {
		t.Fatalf("deriveMetrics mutated input samples")
	}
}

func TestDeriveMetricsGrowthAndExhaustion(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	prev := domain.Snapshot{
		Timestamp: base,
		Postgres:  domain.PostgresMetrics{MaxConnections: 100, ActiveConnections: 50, WaitingConnections: 1},
		PgBouncer: domain.PgBouncerMetrics{WaitingClients: 1, ActiveClients: 10},
	}
	int64p := func(v int64) *int64 { return &v }

	tests := []struct {
		name          string
		history       []domain.Snapshot
		current       domain.Snapshot
		wantQueueRate float64
		wantConnRate  float64
		wantETA       *int64
	}{
		{
			name:    "queue growing with capacity",
			history: []domain.Snapshot{prev},
			current: domain.Snapshot{
				Timestamp: base.Add(2 * time.Second),
				Postgres:  domain.PostgresMetrics{MaxConnections: 100, ActiveConnections: 60, WaitingConnections: 3},
				PgBouncer: domain.PgBouncerMetrics{WaitingClients: 9, ActiveClients: 14},
			},
			wantQueueRate: 5,         // (12-2)/2
			wantConnRate:  7,         // (74-60)/2
			wantETA:       int64p(8), // capacity 40 / 5 per sec
		},
		{
			name:    "capacity exhausted gives zero",
			history: []domain.Snapshot{prev},
			current: domain.Snapshot{
				Timestamp: base.Add(time.Second),
				Postgres:  domain.PostgresMetrics{MaxConnections: 100, ActiveConnections: 100, WaitingConnections: 2},
				PgBouncer: domain.PgBouncerMetrics{WaitingClients: 2, ActiveClients: 10},
			},
			wantQueueRate: 2,
			wantConnRate:  50,
			wantETA:       int64p(0),
		},
		{
			name:    "no max connections gives zero",
			history: []domain.Snapshot{prev},
			current: domain.Snapshot{
				Timestamp: base.Add(time.Second),
				PgBouncer: domain.PgBouncerMetrics{WaitingClients: 5, ActiveClients: 10},
			},
			wantQueueRate: 3,
			wantConnRate:  -50,
			wantETA:       int64p(0),
		},
		{
			name:    "queue shrinking has no ETA",
			history: []domain.Snapshot{prev},
			current: domain.Snapshot{
				Timestamp: base.Add(time.Second),
				Postgres:  domain.PostgresMetrics{MaxConnections: 100, ActiveConnections: 50},
				PgBouncer: domain.PgBouncerMetrics{ActiveClients: 10},
			},
			wantQueueRate: -2,
			wantConnRate:  0,
			wantETA:       nil,
		},
		{
			name:    "non-increasing timestamp ignored",
			history: []domain.Snapshot{prev},
			current: domain.Snapshot{
				Timestamp: base,
				Postgres:  domain.PostgresMetrics{MaxConnections: 100, WaitingConnections: 50},
			},
			wantETA: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveMetrics(tc.history, tc.current)
			if !approx(got.QueueGrowthPerSecond, tc.wantQueueRate) {
				t.Errorf("QueueGrowthPerSecond = %v, want %v", got.QueueGrowthPerSecond, tc.wantQueueRate)
			}
			if !approx(got.ConnectionGrowthPerSec, tc.wantConnRate) {
				t.Errorf("ConnectionGrowthPerSec = %v, want %v", got.ConnectionGrowthPerSec, tc.wantConnRate)
			}
			switch {
			case tc.wantETA == nil && got.ExhaustionSeconds != nil:
				t.Errorf("ExhaustionSeconds = %d, want nil", *got.ExhaustionSeconds)
			case tc.wantETA != nil && got.ExhaustionSeconds == nil:
				t.Errorf("ExhaustionSeconds = nil, want %d", *tc.wantETA)
			case tc.wantETA != nil && *got.ExhaustionSeconds != *tc.wantETA:
				t.Errorf("ExhaustionSeconds = %d, want %d", *got.ExhaustionSeconds, *tc.wantETA)
			}
		})
	}
}
