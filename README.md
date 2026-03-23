# PoolWatch

PoolWatch is a passive-first connection pool observability service for PostgreSQL and pgBouncer.

## Modes

- `passive`: collects PostgreSQL and pgBouncer metrics without intercepting traffic
- `assisted`: adds asynchronous analysis and predictive alerts
- `proxy`: enables the guarded TCP proxy module for temporary debugging

## Endpoints

- `GET /healthz`
- `GET /api/v1/metrics`
- `GET /api/v1/alerts`
- `GET /api/v1/status`
- `POST /api/v1/proxy/enable`
- `POST /api/v1/proxy/disable`

## Environment

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

## Run

```bash
go run ./cmd/poolwatch
```

## Docker

```bash
docker build -t poolwatch .
docker run --env-file .env.example -p 8080:8080 -p 6433:6433 poolwatch
```
