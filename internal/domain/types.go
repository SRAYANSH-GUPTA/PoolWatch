package domain

import "time"

type Snapshot struct {
	Timestamp         time.Time          `json:"timestamp"`
	Postgres          PostgresMetrics    `json:"postgres"`
	PgBouncer         PgBouncerMetrics   `json:"pgbouncer"`
	Proxy             ProxyMetrics       `json:"proxy"`
	Derived           DerivedMetrics     `json:"derived"`
	CollectorErrors   map[string]string  `json:"collector_errors,omitempty"`
	SourceLatenciesMS map[string]float64 `json:"source_latencies_ms,omitempty"`
	Metadata          map[string]string  `json:"metadata,omitempty"`
}

type PostgresMetrics struct {
	MaxConnections     int                `json:"max_connections"`
	ActiveConnections  int                `json:"active_connections"`
	IdleConnections    int                `json:"idle_connections"`
	IdleInTxn          int                `json:"idle_in_transaction"`
	WaitingConnections int                `json:"waiting_connections"`
	LongestTxn         time.Duration      `json:"longest_txn"`
	LongestQuery       time.Duration      `json:"longest_query"`
	QueryHogs          []QueryHog         `json:"query_hogs"`
	LeakCandidates     []LeakCandidate    `json:"leak_candidates"`
	TopStatements      []StatementMetrics `json:"top_statements"`
}

type PgBouncerMetrics struct {
	Databases       []PoolDatabaseStats `json:"databases"`
	ActiveClients   int                 `json:"active_clients"`
	WaitingClients  int                 `json:"waiting_clients"`
	ActiveServers   int                 `json:"active_servers"`
	IdleServers     int                 `json:"idle_servers"`
	MaxWait         time.Duration       `json:"max_wait"`
	AvgWait         time.Duration       `json:"avg_wait"`
	WaitSamples     []time.Duration     `json:"wait_samples"`
	ConnectionChurn int64               `json:"connection_churn"`
}

type PoolDatabaseStats struct {
	Database       string `json:"database"`
	User           string `json:"user"`
	ClActive       int    `json:"cl_active"`
	ClWaiting      int    `json:"cl_waiting"`
	SvActive       int    `json:"sv_active"`
	SvIdle         int    `json:"sv_idle"`
	PoolMode       string `json:"pool_mode"`
	MaxwaitSeconds int64  `json:"maxwait_seconds"`
}

type QueryHog struct {
	PID         int           `json:"pid"`
	Database    string        `json:"database"`
	Application string        `json:"application"`
	ClientAddr  string        `json:"client_addr"`
	Duration    time.Duration `json:"duration"`
	Query       string        `json:"query"`
	State       string        `json:"state"`
}

type LeakCandidate struct {
	PID         int           `json:"pid"`
	Database    string        `json:"database"`
	Application string        `json:"application"`
	ClientAddr  string        `json:"client_addr"`
	IdleFor     time.Duration `json:"idle_for"`
	InTxn       bool          `json:"in_txn"`
	LastQuery   string        `json:"last_query"`
}

type StatementMetrics struct {
	QueryID       string        `json:"query_id"`
	Query         string        `json:"query"`
	Calls         int64         `json:"calls"`
	MeanExecTime  time.Duration `json:"mean_exec_time"`
	TotalExecTime time.Duration `json:"total_exec_time"`
}

type ProxyMetrics struct {
	Enabled             bool          `json:"enabled"`
	Healthy             bool          `json:"healthy"`
	OpenConnections     int64         `json:"open_connections"`
	AcceptedConnections int64         `json:"accepted_connections"`
	ClosedConnections   int64         `json:"closed_connections"`
	IngressBytes        int64         `json:"ingress_bytes"`
	EgressBytes         int64         `json:"egress_bytes"`
	P95SessionLatency   time.Duration `json:"p95_session_latency"`
	LastError           string        `json:"last_error,omitempty"`
}

type DerivedMetrics struct {
	PoolUsage              float64       `json:"pool_usage"`
	WaitP50                time.Duration `json:"wait_p50"`
	WaitP95                time.Duration `json:"wait_p95"`
	WaitP99                time.Duration `json:"wait_p99"`
	ExhaustionSeconds      *int64        `json:"exhaustion_seconds,omitempty"`
	QueueGrowthPerSecond   float64       `json:"queue_growth_per_second"`
	ConnectionGrowthPerSec float64       `json:"connection_growth_per_second"`
}

type AlertSeverity string

const (
	SeverityInfo  AlertSeverity = "info"
	SeverityWarn  AlertSeverity = "warn"
	SeverityError AlertSeverity = "error"
)

type Alert struct {
	Timestamp time.Time      `json:"timestamp"`
	Severity  AlertSeverity  `json:"severity"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
}

type Event struct {
	Type      string         `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	Snapshot  *Snapshot      `json:"snapshot,omitempty"`
	Alert     *Alert         `json:"alert,omitempty"`
	Values    map[string]any `json:"values,omitempty"`
}
