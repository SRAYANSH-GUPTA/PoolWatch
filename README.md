# PoolWatch

PoolWatch is a passive-first connection-pool observability service for PostgreSQL and pgBouncer.

It helps backend teams understand pool pressure, detect query/connection bottlenecks, and react before incidents become outages.

## Why PoolWatch

Connection-pool incidents are often discovered too late. A queue starts growing, p95 wait times spike, and application latency follows.

PoolWatch gives your backend a focused control plane for pool health:

- continuous PostgreSQL + pgBouncer collection
- derived signals like pool usage and queue growth
- alert generation (threshold + predictive)
- optional guarded proxy mode for temporary debugging

## How It Helps Your Backend

Use PoolWatch as an external telemetry source for your backend services:

- monitor pool saturation in near real-time
- trigger autoscaling or traffic shaping when queue growth accelerates
- detect likely root causes (long-running query hogs, leak candidates)
- surface operational alerts to your existing incident stack (Slack, PagerDuty, Opsgenie)
- reduce MTTR during pool-pressure events with centralized status data

## Advantages

- Passive-first by default: no traffic interception needed in normal operation
- Predictive signals: includes pool exhaustion risk via growth-rate analysis
- Focused API: simple HTTP endpoints for status, metrics, and alerts
- Safe debugging mode: guarded proxy can be enabled/disabled via API
- Lightweight deployment: single Go binary or Docker container
- Backend-agnostic: integrate from any language that can call HTTP

## Operating Modes

- `passive`: collect PostgreSQL and pgBouncer metrics without intercepting traffic
- `assisted`: add asynchronous analysis and predictive alerts
- `proxy`: enable guarded TCP proxy module for temporary debugging

## Architecture At A Glance

1. Collector gathers PostgreSQL and pgBouncer metrics on a fixed interval.
2. Analyzer derives pool/queue trends and emits alerts.
3. API serves health, metrics, alerts, and runtime state.
4. Proxy service can be toggled for controlled investigation.

## API Endpoints

- `GET /` Dashboard view
- `GET /healthz` Service liveness and internal state
- `GET /api/v1/metrics` Latest snapshot
- `GET /api/v1/alerts` Current alert list
- `GET /api/v1/status` Runtime config + state
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
curl http://localhost:8080/api/v1/status
curl http://localhost:8080/api/v1/metrics
curl http://localhost:8080/api/v1/alerts
```

### 3) Run with Docker

```bash
docker build -t poolwatch .
docker run --env-file .env.example -p 8080:8080 -p 6433:6433 poolwatch
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

Useful alert codes include:

- `pool_usage_high`
- `wait_time_high`
- `pool_exhaustion_predicted`
- `query_hog_detected`
- `connection_leak_candidate`
- `root_cause_pool_exhaustion`
- `proxy_unhealthy`

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

Environment variables (see `.env.example` for defaults):

- `POOLWATCH_MODE`
- `POOLWATCH_HTTP_ADDR`
- `POOLWATCH_PROXY_LISTEN_ADDR`
- `POOLWATCH_PROXY_UPSTREAM_ADDR`
- `POOLWATCH_POSTGRES_DSN`
- `POOLWATCH_PGBOUNCER_DSN`
- `POOLWATCH_COLLECT_INTERVAL`
- `POOLWATCH_ANALYZE_INTERVAL`
- `POOLWATCH_EVENT_QUEUE_SIZE`
- `POOLWATCH_HISTORY_LIMIT`
- `POOLWATCH_POOL_USAGE_THRESHOLD`
- `POOLWATCH_WAIT_P95_THRESHOLD`
- `POOLWATCH_LONG_QUERY_THRESHOLD`
- `POOLWATCH_LEAK_THRESHOLD`
- `POOLWATCH_PROXY_LATENCY_LIMIT`
- `POOLWATCH_PROXY_QUEUE_LIMIT`
- `POOLWATCH_PROXY_CONNECTION_LIMIT`
- `POOLWATCH_LOG_LEVEL`

## Production Notes

- Run in `passive` mode first, then enable `assisted` once thresholds are tuned.
- Keep DSNs least-privileged where possible.
- Start with conservative alert thresholds and adjust based on baseline traffic.
- Prefer integrating alerts into your existing incident channel rather than creating a separate process.

## Roadmap Ideas

- native webhook sink for alerts
- Prometheus metrics endpoint
- OpenTelemetry export
- service-level anomaly profiles
