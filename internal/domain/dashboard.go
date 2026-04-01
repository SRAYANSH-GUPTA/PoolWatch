package domain

type DashboardData struct {
	Status  DashboardStatus  `json:"status"`
	Summary DashboardKPI     `json:"summary"`
	Series  []DashboardPoint `json:"series"`
	Alerts  []Alert          `json:"alerts"`
	Tables  DashboardTables  `json:"tables"`
}

type DashboardStatus struct {
	Mode            string `json:"mode"`
	ProxyEnabled    bool   `json:"proxy_enabled"`
	ProxyHealthy    bool   `json:"proxy_healthy"`
	MongoDBEnabled  bool   `json:"mongodb_enabled"`
	LastCollectedAt string `json:"last_collected_at"`
}

type DashboardKPI struct {
	PoolUsagePercent     float64 `json:"pool_usage_percent"`
	WaitP95MS            float64 `json:"wait_p95_ms"`
	WaitingClients       int     `json:"waiting_clients"`
	ActiveConnections    int     `json:"active_connections"`
	QueueGrowthPerSecond float64 `json:"queue_growth_per_second"`
	ExhaustionSeconds    *int64  `json:"exhaustion_seconds,omitempty"`
}

type DashboardPoint struct {
	Timestamp            string  `json:"timestamp"`
	PoolUsagePercent     float64 `json:"pool_usage_percent"`
	WaitP95MS            float64 `json:"wait_p95_ms"`
	WaitingClients       int     `json:"waiting_clients"`
	ActiveConnections    int     `json:"active_connections"`
	QueueGrowthPerSecond float64 `json:"queue_growth_per_second"`
	ProxyHealthy         bool    `json:"proxy_healthy"`
}

type DashboardTables struct {
	QueryHogs      []QueryHog         `json:"query_hogs"`
	LeakCandidates []LeakCandidate    `json:"leak_candidates"`
	TopStatements  []StatementMetrics `json:"top_statements"`
}
