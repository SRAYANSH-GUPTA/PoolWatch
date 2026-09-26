package api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"poolwatch/internal/domain"
)

const prometheusContentType = "text/plain; version=0.0.4; charset=utf-8"

type promWriter struct {
	b strings.Builder
}

type promSample struct {
	labels [][2]string
	value  float64
}

func (p *promWriter) family(name, help, kind string, samples ...promSample) {
	fmt.Fprintf(&p.b, "# HELP %s %s\n# TYPE %s %s\n", name, escapeHelp(help), name, kind)
	for _, sample := range samples {
		p.b.WriteString(name)
		if len(sample.labels) > 0 {
			p.b.WriteByte('{')
			for i, label := range sample.labels {
				if i > 0 {
					p.b.WriteByte(',')
				}
				fmt.Fprintf(&p.b, "%s=\"%s\"", label[0], escapeLabelValue(label[1]))
			}
			p.b.WriteByte('}')
		}
		p.b.WriteByte(' ')
		p.b.WriteString(formatPromFloat(sample.value))
		p.b.WriteByte('\n')
	}
}

func (p *promWriter) gauge(name, help string, value float64) {
	p.family(name, help, "gauge", promSample{value: value})
}

func escapeHelp(value string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(value)
}

func escapeLabelValue(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(value)
}

func formatPromFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func seconds(d time.Duration) float64 {
	return d.Seconds()
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// renderPrometheus renders a snapshot and alert counters in text exposition format 0.0.4.
func renderPrometheus(snapshot domain.Snapshot, alertCounts map[string]uint64) string {
	p := &promWriter{}
	pg := snapshot.Postgres
	pb := snapshot.PgBouncer
	d := snapshot.Derived

	p.gauge("poolwatch_pool_usage_ratio", "Derived pool usage ratio (0-1).", d.PoolUsage)
	p.gauge("poolwatch_postgres_max_connections", "PostgreSQL max_connections.", float64(pg.MaxConnections))
	p.gauge("poolwatch_postgres_active_connections", "PostgreSQL active connections.", float64(pg.ActiveConnections))
	p.gauge("poolwatch_postgres_idle_connections", "PostgreSQL idle connections.", float64(pg.IdleConnections))
	p.gauge("poolwatch_postgres_idle_in_txn_connections", "PostgreSQL idle-in-transaction connections.", float64(pg.IdleInTxn))
	p.gauge("poolwatch_postgres_waiting_connections", "PostgreSQL connections waiting on locks.", float64(pg.WaitingConnections))
	p.gauge("poolwatch_postgres_longest_query_seconds", "Duration of the longest running PostgreSQL query.", seconds(pg.LongestQuery))
	p.gauge("poolwatch_postgres_query_hogs", "Number of long-running queries holding connections.", float64(len(pg.QueryHogs)))
	p.gauge("poolwatch_postgres_leak_candidates", "Number of possibly leaked connections.", float64(len(pg.LeakCandidates)))
	if len(pg.AppConnections) > 0 {
		samples := make([]promSample, 0, len(pg.AppConnections)*3)
		for _, app := range pg.AppConnections {
			for _, st := range []struct {
				state string
				value int
			}{{"total", app.Total}, {"active", app.Active}, {"idle_in_txn", app.IdleInTxn}} {
				samples = append(samples, promSample{
					labels: [][2]string{{"application", app.Application}, {"database", app.Database}, {"state", st.state}},
					value:  float64(st.value),
				})
			}
		}
		p.family("poolwatch_postgres_app_connections", "PostgreSQL connections per client application and database.", "gauge", samples...)
	}
	p.gauge("poolwatch_pgbouncer_active_clients", "pgBouncer active client connections.", float64(pb.ActiveClients))
	p.gauge("poolwatch_pgbouncer_waiting_clients", "pgBouncer waiting client connections.", float64(pb.WaitingClients))
	p.gauge("poolwatch_pgbouncer_active_servers", "pgBouncer active server connections.", float64(pb.ActiveServers))
	p.gauge("poolwatch_pgbouncer_idle_servers", "pgBouncer idle server connections.", float64(pb.IdleServers))
	p.gauge("poolwatch_pgbouncer_max_wait_seconds", "pgBouncer maximum client wait time.", seconds(pb.MaxWait))
	p.gauge("poolwatch_wait_p50_seconds", "p50 connection wait time.", seconds(d.WaitP50))
	p.gauge("poolwatch_wait_p95_seconds", "p95 connection wait time.", seconds(d.WaitP95))
	p.gauge("poolwatch_wait_p99_seconds", "p99 connection wait time.", seconds(d.WaitP99))
	p.gauge("poolwatch_queue_growth_per_second", "Waiting client queue growth rate per second.", d.QueueGrowthPerSecond)
	p.gauge("poolwatch_connection_growth_per_second", "Connection growth rate per second.", d.ConnectionGrowthPerSec)
	if d.ExhaustionSeconds != nil {
		p.gauge("poolwatch_exhaustion_seconds", "Predicted seconds until pool exhaustion.", float64(*d.ExhaustionSeconds))
	}
	p.gauge("poolwatch_proxy_enabled", "Whether guarded proxy mode is enabled.", boolFloat(snapshot.Proxy.Enabled))
	p.gauge("poolwatch_proxy_healthy", "Whether guarded proxy mode is healthy.", boolFloat(snapshot.Proxy.Healthy))
	p.gauge("poolwatch_proxy_open_connections", "Open proxied connections.", float64(snapshot.Proxy.OpenConnections))

	if len(pb.Databases) > 0 {
		samples := make([]promSample, 0, len(pb.Databases))
		for _, db := range pb.Databases {
			samples = append(samples, promSample{
				labels: [][2]string{{"database", db.Database}, {"user", db.User}},
				value:  float64(db.ClWaiting),
			})
		}
		p.family("poolwatch_pgbouncer_pool_waiting_clients", "pgBouncer waiting clients per pool.", "gauge", samples...)
	}

	sources := map[string]struct{}{}
	for source := range snapshot.SourceLatenciesMS {
		sources[source] = struct{}{}
	}
	for source := range snapshot.CollectorErrors {
		sources[source] = struct{}{}
	}
	if len(sources) > 0 {
		samples := make([]promSample, 0, len(sources))
		for _, source := range sortedKeys(sources) {
			_, failed := snapshot.CollectorErrors[source]
			samples = append(samples, promSample{labels: [][2]string{{"source", source}}, value: boolFloat(!failed)})
		}
		p.family("poolwatch_collector_up", "Whether the last collection from a source succeeded.", "gauge", samples...)
	}

	if len(snapshot.SourceLatenciesMS) > 0 {
		samples := make([]promSample, 0, len(snapshot.SourceLatenciesMS))
		for _, source := range sortedKeys(snapshot.SourceLatenciesMS) {
			samples = append(samples, promSample{
				labels: [][2]string{{"source", source}},
				value:  snapshot.SourceLatenciesMS[source] / 1000,
			})
		}
		p.family("poolwatch_source_latency_seconds", "Collection latency per source.", "gauge", samples...)
	}

	samples := make([]promSample, 0, len(alertCounts))
	for _, code := range sortedKeys(alertCounts) {
		samples = append(samples, promSample{labels: [][2]string{{"code", code}}, value: float64(alertCounts[code])})
	}
	p.family("poolwatch_alerts_total", "Total alerts emitted by code.", "counter", samples...)

	return p.b.String()
}

func (s *Server) prometheus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body := renderPrometheus(s.state.Snapshot(), s.state.AlertCounts())
	w.Header().Set("Content-Type", prometheusContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	snapshot := s.state.Snapshot()
	if snapshot.Timestamp.IsZero() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "reason": "no snapshot collected yet"})
		return
	}
	maxAge := 3 * s.cfg.CollectInterval
	age := time.Since(snapshot.Timestamp)
	if maxAge > 0 && age > maxAge {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":  "not_ready",
			"reason":  "latest snapshot is stale",
			"age":     age.Truncate(time.Millisecond).String(),
			"max_age": maxAge.String(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "age": age.Truncate(time.Millisecond).String()})
}
