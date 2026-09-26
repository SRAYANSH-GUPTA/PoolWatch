# PoolWatch

PoolWatch is a passive-first connection-pool observability service for PostgreSQL, pgBouncer and MongoDB.

It helps backend teams understand pool pressure, detect query/connection bottlenecks, and react before incidents become outages.

## Why PoolWatch

Connection-pool incidents are often discovered too late. A queue starts growing, p95 wait times spike, and application latency follows.

PoolWatch gives your backend a focused control plane for pool health:

- continuous PostgreSQL + pgBouncer + MongoDB pool collection
- derived signals like pool usage and queue growth
- alert generation (threshold + predictive + trend regression)
- Prometheus `/metrics` exposition and alert webhooks (generic JSON or Slack)
- optional guarded proxy mode for temporary debugging

## How It Helps Your Backend

Use PoolWatch as an external telemetry source for your backend services:

- monitor pool saturation in near real-time
- trigger autoscaling or traffic shaping when queue growth accelerates
- detect likely root causes (long-running query hogs, leak candidates)
- attribute PostgreSQL connections per application (`pg_stat_activity.application_name`), exported as `poolwatch_postgres_app_connections{application,database,state}`
- surface operational alerts to your existing incident stack (Slack, PagerDuty, Opsgenie)
- reduce MTTR during pool-pressure events with centralized status data

## Advantages

- Passive-first by default: no traffic interception needed in normal operation
- Predictive signals: pool exhaustion risk via growth-rate analysis plus least-squares trend forecasting
- Native integrations: Prometheus scrape endpoint, readiness probe and deduplicated alert webhooks
- Focused API: simple HTTP endpoints for status, metrics, and alerts
- Safe debugging mode: guarded proxy can be enabled/disabled via API
- Lightweight deployment: single Go binary or Docker container
- Backend-agnostic: integrate from any language that can call HTTP

## Operating Modes

- `passive`: collect PostgreSQL, pgBouncer and MongoDB pool metrics without intercepting traffic
- `assisted`: add asynchronous analysis, predictive/trend alerts and webhook delivery
- `proxy`: enable guarded TCP proxy module for temporary debugging

## Architecture At A Glance

1. Collector gathers PostgreSQL, pgBouncer and (optionally) MongoDB pool metrics on a fixed interval.
2. Analyzer derives pool/queue signals, fits least-squares trends over a sliding window, and emits alerts.
3. Alert sink fans alerts out to MongoDB history and an optional webhook, with per-code cooldown dedup.
4. API serves health, readiness, Prometheus metrics, alerts, and runtime state.
5. Proxy service can be toggled for controlled investigation.

## MongoDB Pool Monitoring

Set `POOLWATCH_MONGO_MONITOR_URI` to monitor a MongoDB deployment's connection pool (this is separate from `POOLWATCH_MONGODB_URI`, which stores PoolWatch's own history). Each collection cycle samples:

- `serverStatus.connections` (current, available, totalCreated, active) to derive server-side pool usage
- driver connection-pool events: checkout wait time and checkout failures
- slow in-flight operations via `currentOp` (running longer than `POOLWATCH_LONG_QUERY_THRESHOLD`)

Related alerts: `mongo_pool_usage_high`, `mongo_checkout_failures`, `mongo_slow_operation`.

## Trend-Based Prediction

In `assisted` mode the analyzer fits a least-squares linear regression over the samples in `POOLWATCH_TREND_WINDOW` (default `2m`) and projects each series forward over `POOLWATCH_TREND_HORIZON` (default `5m`):

- pool usage slope projected to cross 100% within the horizon -> `pool_usage_trend_exhaustion`
- p95 wait time slope projected past `POOLWATCH_WAIT_P95_THRESHOLD` -> `wait_time_trend_rising`
- waiting-client queue depth growing steadily -> `queue_depth_trend_rising`

Regression smooths out single-sample spikes, so trend alerts fire on sustained drift rather than noise, complementing the instantaneous `pool_exhaustion_predicted` growth-rate check.

## Alert Catalog

| Code | Severity | Trigger |
| --- | --- | --- |
| `pool_usage_high` | warn | Derived pool usage >= `POOLWATCH_POOL_USAGE_THRESHOLD` |
| `wait_time_high` | warn | p95 client wait >= `POOLWATCH_WAIT_P95_THRESHOLD` |
| `pool_exhaustion_predicted` | error | Queue growth rate projects pool exhaustion shortly |
| `query_hog_detected` | warn | Active query running longer than `POOLWATCH_LONG_QUERY_THRESHOLD` |
| `connection_leak_candidate` | warn | Idle / idle-in-transaction connection older than `POOLWATCH_LEAK_THRESHOLD` |
| `root_cause_pool_exhaustion` | error | Pool pressure correlated with query hogs or leak candidates |
| `proxy_unhealthy` | error | Guarded proxy exceeded latency, queue or connection limits and was disabled |
| `pool_usage_trend_exhaustion` | error | Regression over `POOLWATCH_TREND_WINDOW` projects usage >= 100% within `POOLWATCH_TREND_HORIZON` |
| `wait_time_trend_rising` | warn | Regression projects p95 wait above threshold within the horizon |
| `queue_depth_trend_rising` | warn | Waiting-client queue depth shows a sustained positive slope |
| `mongo_pool_usage_high` | warn | MongoDB current / (current + available) connections >= `POOLWATCH_POOL_USAGE_THRESHOLD` |
| `mongo_checkout_failures` | error | Driver pool checkout failures observed in the last interval |
| `mongo_slow_operation` | warn | `currentOp` reports an operation running longer than `POOLWATCH_LONG_QUERY_THRESHOLD` |
| `app_connection_hog` | warn | A single application (by `application_name` + database) holds >= 50% of PostgreSQL `max_connections` |

## Alert Webhooks

Set `POOLWATCH_ALERT_WEBHOOK_URL` to push alerts as they fire:

- `POOLWATCH_ALERT_WEBHOOK_FORMAT=generic` (default) posts the alert JSON as-is
- `POOLWATCH_ALERT_WEBHOOK_FORMAT=slack` posts a Slack incoming-webhook `{"text": ...}` payload
- repeated alerts with the same code are suppressed for `POOLWATCH_ALERT_COOLDOWN` (default `1m`)

## API Endpoints

- `GET /` Dashboard view
- `GET /healthz` Service liveness and internal state
- `GET /readyz` Readiness probe (returns `503` when the latest snapshot is stale)
- `GET /metrics` Prometheus text exposition
- `GET /api/v1/metrics` Latest snapshot
- `GET /api/v1/alerts` Current alert list
- `GET /api/v1/status` Runtime config + state
- `GET /api/v1/history?window=15m&limit=300` Snapshot history for charts
- `GET /api/v1/series?window=15m&limit=300` Compact time-series feed for dashboards
- `GET /api/v1/dashboard?window=15m&limit=180` Frontend-oriented dashboard payload
- `POST /api/v1/proxy/enable` Enable guarded proxy mode
- `POST /api/v1/proxy/disable` Disable guarded proxy mode

## Quick Start

### 1) Run locally

```bash
cp .env.example .env
set -a
source .env
set +a

go run ./cmd/poolwatch
```

### 2) Verify

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
curl http://localhost:8080/metrics
curl http://localhost:8080/api/v1/status
curl http://localhost:8080/api/v1/metrics
curl http://localhost:8080/api/v1/alerts
curl 'http://localhost:8080/api/v1/history?window=15m&limit=120'
curl 'http://localhost:8080/api/v1/series?window=15m&limit=120'
curl 'http://localhost:8080/api/v1/dashboard?window=15m&limit=120'
```

### 3) Run with Docker

```bash
docker build -t poolwatch .
docker run --env-file .env.example -p 8080:8080 -p 6433:6433 poolwatch
```

The image is a static binary on `gcr.io/distroless/static-debian12:nonroot` (no shell), so health is checked by the orchestrator: point liveness at `/healthz` and readiness at `/readyz`.

### 4) Full stack with Docker Compose

`docker-compose.yml` brings up PostgreSQL 16 (with `pg_stat_statements`, `max_connections=50` and a `poolwatch` role granted `pg_monitor`), pgBouncer (transaction pooling on `:6432`), MongoDB 7 and PoolWatch in `assisted` mode.

```bash
make up      # docker compose up -d --build
make load    # adds a pgbench loop against pgBouncer to create pool pressure
make down    # tear everything down
```

Then open `http://localhost:8080/` and watch the alerts roll in.

### Makefile targets

| Target | Description |
| --- | --- |
| `make build` | Build a static `./poolwatch` binary |
| `make run` | `go run ./cmd/poolwatch` |
| `make test` | `go test -race ./...` |
| `make vet` | `go vet ./...` |
| `make lint` | Fail if `gofmt -l` reports unformatted files |
| `make docker-build` | Build the `poolwatch:local` image |
| `make up` / `make down` | Start / stop the compose stack |
| `make load` | `docker compose --profile load up` (pgbench pressure) |
| `make clean` | Remove the binary, `coverage.out` and `dist/` |

### Prometheus scrape config

```yaml
scrape_configs:
  - job_name: poolwatch
    scrape_interval: 15s
    metrics_path: /metrics
    static_configs:
      - targets: ["poolwatch:8080"]
```

## Integrate With Your Backend

You can integrate PoolWatch in 3 practical ways.

### 1) Polling Integration (simplest)

Your backend periodically calls:

- `/api/v1/metrics` for live pool state
- `/api/v1/alerts` for active signal feed
- `/api/v1/status` for runtime health checks

Use this data to:

- expose internal health endpoints with pool posture
- gate expensive jobs when pool usage is high
- trigger autoscaling policies from queue growth

### 2) Alert Bridge Integration

Create a small worker in your backend that pulls `/api/v1/alerts` and forwards new alerts to your alerting platform.

Recommended dedupe key:

- `code + timestamp + severity`

See the [Alert Catalog](#alert-catalog) for every code. If you only need push delivery, the built-in webhook sink (`POOLWATCH_ALERT_WEBHOOK_URL`) replaces this worker entirely.

### 3) Operational Control Integration

During incident response, your backend operations tooling can call:

- `POST /api/v1/proxy/enable`
- `POST /api/v1/proxy/disable`

This allows temporary deep debugging without permanently changing service traffic architecture.

## Example Backend Polling Snippet (Node.js)

```js
const baseUrl = process.env.POOLWATCH_URL || "http://localhost:8080";

async function fetchPoolStatus() {
	const [metricsRes, alertsRes] = await Promise.all([
		fetch(`${baseUrl}/api/v1/metrics`),
		fetch(`${baseUrl}/api/v1/alerts`),
	]);

	const metrics = await metricsRes.json();
	const alerts = await alertsRes.json();

	return {
		poolUsage: metrics?.derived?.pool_usage ?? 0,
		waitP95: metrics?.derived?.wait_p95,
		waitingClients: (metrics?.pgbouncer?.waiting_clients ?? 0) + (metrics?.postgres?.waiting_connections ?? 0),
		alerts: alerts?.alerts ?? [],
	};
}
```

## Configuration

See the annotated `.env.example` for defaults and inline explanations. Durations use Go syntax (`250ms`, `2s`, `5m`, `168h`).

| Variable | Default | Description |
| --- | --- | --- |
| `POOLWATCH_MODE` | `passive` | `passive`, `assisted` or `proxy` |
| `POOLWATCH_HTTP_ADDR` | `:8080` | HTTP API / dashboard bind address |
| `POOLWATCH_PROXY_LISTEN_ADDR` | `:6433` | Guarded proxy listen address |
| `POOLWATCH_PROXY_UPSTREAM_ADDR` | `127.0.0.1:6432` | Proxy upstream, usually pgBouncer |
| `POOLWATCH_POSTGRES_DSN` | _(empty)_ | PostgreSQL DSN for pool and query observability |
| `POOLWATCH_PGBOUNCER_DSN` | _(empty)_ | pgBouncer admin DSN (database `pgbouncer`) for `SHOW POOLS` / `SHOW STATS` |
| `POOLWATCH_MONGO_MONITOR_URI` | _(empty)_ | MongoDB deployment whose connection pool is monitored; empty disables |
| `POOLWATCH_MONGODB_URI` | _(empty)_ | MongoDB for PoolWatch history; empty keeps history in memory |
| `POOLWATCH_MONGODB_DATABASE` | `poolwatch` | History database |
| `POOLWATCH_MONGODB_SNAPSHOTS_COLLECTION` | `snapshots` | Snapshot collection |
| `POOLWATCH_MONGODB_ALERTS_COLLECTION` | `alerts` | Alert collection |
| `POOLWATCH_MONGODB_RETENTION` | `168h` | TTL for history documents (min `1h`) |
| `POOLWATCH_COLLECT_INTERVAL` | `2s` | Collector sampling interval |
| `POOLWATCH_ANALYZE_INTERVAL` | `5s` | Reserved analysis interval (analyzer is event-driven) |
| `POOLWATCH_EVENT_QUEUE_SIZE` | `2048` | Internal event queue size |
| `POOLWATCH_HISTORY_LIMIT` | `180` | Snapshots/alerts kept in memory (min `10`) |
| `POOLWATCH_POOL_USAGE_THRESHOLD` | `0.85` | Pool usage ratio that raises `pool_usage_high` / `mongo_pool_usage_high` |
| `POOLWATCH_WAIT_P95_THRESHOLD` | `250ms` | p95 wait that raises `wait_time_high` |
| `POOLWATCH_LONG_QUERY_THRESHOLD` | `5s` | Long-running query / Mongo op threshold |
| `POOLWATCH_LEAK_THRESHOLD` | `30s` | Idle / idle-in-transaction age for leak candidates |
| `POOLWATCH_TREND_WINDOW` | `2m` | Sliding window for least-squares trend regression |
| `POOLWATCH_TREND_HORIZON` | `5m` | How far ahead trends are projected |
| `POOLWATCH_ALERT_WEBHOOK_URL` | _(empty)_ | Webhook to receive alerts; empty disables |
| `POOLWATCH_ALERT_WEBHOOK_FORMAT` | `generic` | `generic` (raw alert JSON) or `slack` |
| `POOLWATCH_ALERT_COOLDOWN` | `1m` | Suppress repeat alerts with the same code within this window |
| `POOLWATCH_PROXY_LATENCY_LIMIT` | `1ms` | Proxy guard: max p95 session latency |
| `POOLWATCH_PROXY_QUEUE_LIMIT` | `4096` | Proxy guard: max internal queue depth |
| `POOLWATCH_PROXY_CONNECTION_LIMIT` | `10000` | Proxy guard: max concurrent proxied connections |
| `POOLWATCH_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |

Recommended starting point:

- Keep `POOLWATCH_MODE=passive`
- Set both PostgreSQL and pgBouncer DSNs (add `POOLWATCH_MONGO_MONITOR_URI` if you run MongoDB)
- Leave MongoDB disabled until you need historical charts
- Tune thresholds only after observing normal traffic baselines

## Production Notes

- Run in `passive` mode first, then enable `assisted` once thresholds are tuned.
- Keep DSNs least-privileged where possible.
- If using MongoDB Atlas, whitelist your deployment IP or use VPC peering/private endpoints.
- Start with conservative alert thresholds and adjust based on baseline traffic.
- Prefer integrating alerts into your existing incident channel (via `POOLWATCH_ALERT_WEBHOOK_URL`) rather than creating a separate process.
- Scrape `/metrics` with Prometheus and wire `/healthz` / `/readyz` into your orchestrator probes.

## MongoDB Atlas

Set these env vars before starting PoolWatch:

```bash
export POOLWATCH_MONGODB_URI='mongodb+srv://<user>:<password>@<cluster-url>/'
export POOLWATCH_MONGODB_DATABASE='poolwatch'
export POOLWATCH_MONGODB_RETENTION='168h'
```

When MongoDB is configured, PoolWatch will:

- persist every collected snapshot
- persist every generated alert
- expose historical chart data via `/api/v1/history`
- expose compact graph points via `/api/v1/series`

## Dashboard Endpoint

The Flutter app can use a single request:

```bash
curl 'http://localhost:8080/api/v1/dashboard?window=15m&limit=180'
```

Response shape:

```json
{
  "status": {
    "mode": "passive",
    "proxy_enabled": false,
    "proxy_healthy": true,
    "mongodb_enabled": true,
    "last_collected_at": "2026-04-02T12:00:00Z"
  },
  "summary": {
    "pool_usage_percent": 72.3,
    "wait_p95_ms": 18.4,
    "waiting_clients": 4,
    "active_connections": 61,
    "queue_growth_per_second": 0.3,
    "exhaustion_seconds": 93
  },
  "series": [],
  "alerts": [],
  "tables": {
    "query_hogs": [],
    "leak_candidates": [],
    "top_statements": []
  }
}
```

## Roadmap Ideas

- OpenTelemetry export
- service-level anomaly profiles
