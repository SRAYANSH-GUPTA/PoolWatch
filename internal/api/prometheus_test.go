package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"poolwatch/internal/config"
	"poolwatch/internal/domain"
	"poolwatch/internal/store"
)

func testSnapshot(ts time.Time) domain.Snapshot {
	s := domain.Snapshot{
		Timestamp:         ts,
		CollectorErrors:   map[string]string{"postgres": "connection refused"},
		SourceLatenciesMS: map[string]float64{"pgbouncer": 12},
	}
	s.Derived.PoolUsage = 0.5
	s.PgBouncer.Databases = []domain.PoolDatabaseStats{{Database: `app"db`, User: "svc", ClWaiting: 3}}
	return s
}

func newTestServer(snap *domain.Snapshot) *Server {
	st := store.New(10)
	if snap != nil {
		st.UpdateSnapshot(*snap, 10)
	}
	return &Server{cfg: config.Config{CollectInterval: 2 * time.Second}, state: st}
}

func TestPrometheusHandler(t *testing.T) {
	snap := testSnapshot(time.Now())
	s := newTestServer(&snap)
	rec := httptest.NewRecorder()
	s.prometheus(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != prometheusContentType {
		t.Fatalf("content-type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"# TYPE poolwatch_pool_usage_ratio gauge",
		"poolwatch_pool_usage_ratio 0.5\n",
		`poolwatch_collector_up{source="postgres"} 0` + "\n",
		`poolwatch_collector_up{source="pgbouncer"} 1` + "\n",
		`poolwatch_pgbouncer_pool_waiting_clients{database="app\"db",user="svc"} 3` + "\n",
		`poolwatch_source_latency_seconds{source="pgbouncer"} 0.012` + "\n",
		"# TYPE poolwatch_alerts_total counter",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in output:\n%s", want, body)
		}
	}

	rec = httptest.NewRecorder()
	s.prometheus(rec, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", rec.Code)
	}
}

func TestEscapeHelpers(t *testing.T) {
	tests := []struct {
		name, in, label, help string
	}{
		{"plain", "abc", "abc", "abc"},
		{"quote", `a"b`, `a\"b`, `a"b`},
		{"backslash", `a\b`, `a\\b`, `a\\b`},
		{"newline", "a\nb", `a\nb`, `a\nb`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := escapeLabelValue(tc.in); got != tc.label {
				t.Errorf("escapeLabelValue(%q) = %q, want %q", tc.in, got, tc.label)
			}
			if got := escapeHelp(tc.in); got != tc.help {
				t.Errorf("escapeHelp(%q) = %q, want %q", tc.in, got, tc.help)
			}
		})
	}
}

func TestReadyz(t *testing.T) {
	fresh := testSnapshot(time.Now())
	stale := testSnapshot(time.Now().Add(-time.Minute))
	tests := []struct {
		name string
		snap *domain.Snapshot
		want int
	}{
		{"no snapshot", nil, http.StatusServiceUnavailable},
		{"fresh snapshot", &fresh, http.StatusOK},
		{"stale snapshot", &stale, http.StatusServiceUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			newTestServer(tc.snap).ready(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
