package analyzer

import (
	"testing"
	"time"

	"poolwatch/internal/config"
	"poolwatch/internal/domain"
	"poolwatch/internal/store"
)

func newTestService() *Service {
	cfg := config.Config{
		HistoryLimit:       50,
		PoolUsageThreshold: 0.85,
		WaitP95Threshold:   250 * time.Millisecond,
		LongQueryThreshold: 5 * time.Second,
		LeakThreshold:      30 * time.Second,
		TrendWindow:        2 * time.Minute,
		TrendHorizon:       5 * time.Minute,
	}
	return &Service{cfg: cfg, state: store.New(cfg.HistoryLimit)}
}

func healthySnapshot() domain.Snapshot {
	return domain.Snapshot{
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Postgres: domain.PostgresMetrics{
			MaxConnections:    100,
			ActiveConnections: 10,
			QueryHogs:         []domain.QueryHog{{Application: "api", Duration: time.Second}},
			LeakCandidates:    []domain.LeakCandidate{{Application: "worker", IdleFor: 5 * time.Second}},
		},
		Derived: domain.DerivedMetrics{
			PoolUsage: 0.2,
			WaitP95:   10 * time.Millisecond,
		},
	}
}

func TestAnalyzeSnapshot(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(s *domain.Snapshot)
		want   []string
	}{
		{
			name:   "healthy snapshot",
			mutate: func(s *domain.Snapshot) {},
			want:   nil,
		},
		{
			name:   "pool usage high",
			mutate: func(s *domain.Snapshot) { s.Derived.PoolUsage = 0.9 },
			want:   []string{"pool_usage_high"},
		},
		{
			name:   "wait time high",
			mutate: func(s *domain.Snapshot) { s.Derived.WaitP95 = 300 * time.Millisecond },
			want:   []string{"wait_time_high"},
		},
		{
			name: "query hog detected",
			mutate: func(s *domain.Snapshot) {
				s.Postgres.QueryHogs = []domain.QueryHog{{Application: "reports", Database: "app", Duration: 12 * time.Second, Query: "select pg_sleep(12)"}}
			},
			want: []string{"query_hog_detected"},
		},
		{
			name: "connection leak candidate",
			mutate: func(s *domain.Snapshot) {
				s.Postgres.LeakCandidates = []domain.LeakCandidate{
					{Application: "worker", IdleFor: 5 * time.Second},
					{Application: "billing", IdleFor: 2 * time.Minute, InTxn: true},
					{Application: "billing2", IdleFor: 3 * time.Minute},
				}
			},
			want: []string{"connection_leak_candidate"},
		},
		{
			name: "pool exhaustion predicted",
			mutate: func(s *domain.Snapshot) {
				eta := int64(10)
				s.Derived.ExhaustionSeconds = &eta
				s.Derived.QueueGrowthPerSecond = 2
			},
			want: []string{"pool_exhaustion_predicted"},
		},
		{
			name: "proxy unhealthy",
			mutate: func(s *domain.Snapshot) {
				s.Proxy = domain.ProxyMetrics{Enabled: true, Healthy: false, LastError: "boom"}
			},
			want: []string{"proxy_unhealthy"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService()
			snap := healthySnapshot()
			tc.mutate(&snap)
			alerts := svc.analyzeSnapshot(snap)

			got := map[string]int{}
			for _, a := range alerts {
				got[a.Code]++
				if !a.Timestamp.Equal(snap.Timestamp) {
					t.Errorf("alert %q timestamp = %v, want %v", a.Code, a.Timestamp, snap.Timestamp)
				}
			}
			if len(alerts) != len(tc.want) {
				t.Fatalf("got %d alerts %v, want %v", len(alerts), got, tc.want)
			}
			for _, code := range tc.want {
				if got[code] != 1 {
					t.Errorf("expected exactly one %q alert, got %v", code, got)
				}
			}
		})
	}
}

func TestAnalyzeSnapshotLeakDetails(t *testing.T) {
	svc := newTestService()
	snap := healthySnapshot()
	snap.Postgres.LeakCandidates = []domain.LeakCandidate{{Application: "billing", Database: "app", IdleFor: time.Minute, InTxn: true}}
	for _, a := range svc.analyzeSnapshot(snap) {
		if a.Code != "connection_leak_candidate" {
			continue
		}
		if a.Details["application"] != "billing" || a.Details["in_txn"] != true {
			t.Fatalf("unexpected details %v", a.Details)
		}
		return
	}
	t.Fatal("connection_leak_candidate not emitted")
}
