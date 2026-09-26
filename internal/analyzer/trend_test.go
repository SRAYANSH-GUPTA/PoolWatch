package analyzer

import (
	"math"
	"testing"
	"time"

	"poolwatch/internal/config"
	"poolwatch/internal/domain"
	"poolwatch/internal/store"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestLinearFit(t *testing.T) {
	tests := []struct {
		name          string
		xs, ys        []float64
		wantOK        bool
		wantSlope     float64
		wantIntercept float64
		wantR2        float64
	}{
		{"perfect line", []float64{0, 1, 2, 3, 4}, []float64{1, 3, 5, 7, 9}, true, 2, 1, 1},
		{"negative slope", []float64{-4, -3, -2, -1, 0}, []float64{4, 3, 2, 1, 0}, true, -1, 0, 1},
		{"flat line", []float64{0, 1, 2, 3}, []float64{5, 5, 5, 5}, true, 0, 5, 1},
		{"too few points", []float64{1}, []float64{1}, false, 0, 0, 0},
		{"empty", nil, nil, false, 0, 0, 0},
		{"length mismatch", []float64{1, 2, 3}, []float64{1, 2}, false, 0, 0, 0},
		{"identical xs", []float64{2, 2, 2}, []float64{1, 2, 3}, false, 0, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			slope, intercept, r2, ok := linearFit(tc.xs, tc.ys)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if !near(slope, tc.wantSlope) || !near(intercept, tc.wantIntercept) || !near(r2, tc.wantR2) {
				t.Fatalf("got slope=%v intercept=%v r2=%v, want %v %v %v", slope, intercept, r2, tc.wantSlope, tc.wantIntercept, tc.wantR2)
			}
		})
	}
}

func TestLinearFitNoisyR2(t *testing.T) {
	_, _, r2, ok := linearFit([]float64{0, 1, 2, 3, 4, 5}, []float64{0, 5, 0, 5, 0, 5})
	if !ok {
		t.Fatal("expected ok")
	}
	if r2 >= minTrendR2 {
		t.Fatalf("expected low r2 for noisy series, got %v", r2)
	}
}

func TestForecastCrossing(t *testing.T) {
	tests := []struct {
		name                         string
		slope, intercept, atX, limit float64
		wantSeconds                  float64
		wantOK                       bool
	}{
		{"rising reaches limit", 0.01, 0.5, 0, 1.0, 50, true},
		{"relative to later x", 2, 0, 10, 100, 40, true},
		{"already above", 0.01, 1.2, 0, 1.0, 0, true},
		{"flat never crosses", 0, 0.5, 0, 1.0, 0, false},
		{"falling never crosses", -0.1, 0.5, 0, 1.0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := forecastCrossing(tc.slope, tc.intercept, tc.atX, tc.limit)
			if ok != tc.wantOK || !near(got, tc.wantSeconds) {
				t.Fatalf("forecastCrossing = (%v, %v), want (%v, %v)", got, ok, tc.wantSeconds, tc.wantOK)
			}
		})
	}
}

// buildTrendService seeds a store with n snapshots spaced step apart, ending at
// the returned current snapshot, using mutate to fill each snapshot's metrics.
func buildTrendService(n int, step time.Duration, mutate func(i int, s *domain.Snapshot)) (*Service, domain.Snapshot) {
	cfg := config.Config{
		HistoryLimit:     100,
		WaitP95Threshold: 250 * time.Millisecond,
		TrendWindow:      2 * time.Minute,
		TrendHorizon:     5 * time.Minute,
	}
	state := store.New(cfg.HistoryLimit)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var current domain.Snapshot
	for i := 0; i < n; i++ {
		snap := domain.Snapshot{Timestamp: base.Add(time.Duration(i) * step)}
		mutate(i, &snap)
		state.UpdateSnapshot(snap, cfg.HistoryLimit)
		current = snap
	}
	return &Service{cfg: cfg, state: state}, current
}

func alertCodes(alerts []domain.Alert) map[string]domain.Alert {
	out := make(map[string]domain.Alert, len(alerts))
	for _, a := range alerts {
		out[a.Code] = a
	}
	return out
}

func TestTrendAlerts(t *testing.T) {
	tests := []struct {
		name    string
		n       int
		mutate  func(i int, s *domain.Snapshot)
		want    []string
		notWant []string
	}{
		{
			name: "pool usage climbing to exhaustion",
			n:    10,
			mutate: func(i int, s *domain.Snapshot) {
				s.Derived.PoolUsage = 0.40 + 0.02*float64(i) // +0.2%/s at 10s step
			},
			want:    []string{"pool_usage_trend_exhaustion"},
			notWant: []string{"wait_time_trend_rising", "queue_depth_trend_rising"},
		},
		{
			name: "wait p95 rising toward threshold",
			n:    10,
			mutate: func(i int, s *domain.Snapshot) {
				s.Derived.WaitP95 = time.Duration(50+10*i) * time.Millisecond
			},
			want:    []string{"wait_time_trend_rising"},
			notWant: []string{"pool_usage_trend_exhaustion"},
		},
		{
			name: "queue depth growing",
			n:    10,
			mutate: func(i int, s *domain.Snapshot) {
				s.PgBouncer.WaitingClients = i
				s.Postgres.WaitingConnections = i
			},
			want: []string{"queue_depth_trend_rising"},
		},
		{
			name: "flat healthy series",
			n:    10,
			mutate: func(i int, s *domain.Snapshot) {
				s.Derived.PoolUsage = 0.3
				s.Derived.WaitP95 = 10 * time.Millisecond
				s.PgBouncer.WaitingClients = 2
			},
			notWant: []string{"pool_usage_trend_exhaustion", "wait_time_trend_rising", "queue_depth_trend_rising"},
		},
		{
			name: "too few samples",
			n:    4,
			mutate: func(i int, s *domain.Snapshot) {
				s.Derived.PoolUsage = 0.5 + 0.1*float64(i)
			},
			notWant: []string{"pool_usage_trend_exhaustion"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, current := buildTrendService(tc.n, 10*time.Second, tc.mutate)
			codes := alertCodes(svc.trendAlerts(current))
			for _, code := range tc.want {
				if _, ok := codes[code]; !ok {
					t.Errorf("expected alert %q, got %v", code, codes)
				}
			}
			for _, code := range tc.notWant {
				if _, ok := codes[code]; ok {
					t.Errorf("unexpected alert %q", code)
				}
			}
		})
	}
}

func TestTrendAlertsWindowExcludesOldPoints(t *testing.T) {
	// 10 points 30s apart span 4.5m; only the last 5 fall inside a 2m window.
	// If the stale zeros were included, the step 0 -> 0.8 would fit a steep
	// rising line (r2 ~0.76) projecting exhaustion within seconds.
	svc, current := buildTrendService(10, 30*time.Second, func(i int, s *domain.Snapshot) {
		if i >= 5 {
			s.Derived.PoolUsage = 0.8
		}
	})
	if _, ok := alertCodes(svc.trendAlerts(current))["pool_usage_trend_exhaustion"]; ok {
		t.Fatal("stale points outside the trend window influenced the fit")
	}
	svc.cfg.TrendWindow = 10 * time.Minute
	if _, ok := alertCodes(svc.trendAlerts(current))["pool_usage_trend_exhaustion"]; !ok {
		t.Fatal("expected exhaustion alert when the window covers the step")
	}
}
