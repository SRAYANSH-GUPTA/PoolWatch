package collector

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"poolwatch/internal/domain"
)

func (s *Service) collectPostgres(ctx context.Context) (domain.PostgresMetrics, error) {
	result := domain.PostgresMetrics{}

	maxConnections, err := queryInt(ctx, s.postgres, `select current_setting('max_connections')::int`)
	if err != nil {
		return result, err
	}
	result.MaxConnections = maxConnections

	rows, err := s.postgres.QueryContext(ctx, `
		select
			coalesce(state, 'unknown') as state,
			count(*) as total,
			coalesce(max(extract(epoch from now() - xact_start)), 0),
			coalesce(max(extract(epoch from now() - query_start)), 0)
		from pg_stat_activity
		where pid <> pg_backend_pid()
		group by coalesce(state, 'unknown')
	`)
	if err != nil {
		return result, err
	}
	defer rows.Close()

	for rows.Next() {
		var state string
		var total int
		var longestTxnSeconds float64
		var longestQuerySeconds float64
		if err := rows.Scan(&state, &total, &longestTxnSeconds, &longestQuerySeconds); err != nil {
			return result, err
		}
		switch state {
		case "active":
			result.ActiveConnections = total
		case "idle":
			result.IdleConnections = total
		case "idle in transaction":
			result.IdleInTxn = total
		}
		if duration := time.Duration(longestTxnSeconds * float64(time.Second)); duration > result.LongestTxn {
			result.LongestTxn = duration
		}
		if duration := time.Duration(longestQuerySeconds * float64(time.Second)); duration > result.LongestQuery {
			result.LongestQuery = duration
		}
	}

	waiting, err := queryInt(ctx, s.postgres, `select count(*) from pg_stat_activity where wait_event_type is not null and state = 'active'`)
	if err == nil {
		result.WaitingConnections = waiting
	}

	result.QueryHogs, _ = s.collectQueryHogs(ctx)
	result.LeakCandidates, _ = s.collectLeakCandidates(ctx)
	result.TopStatements, _ = s.collectTopStatements(ctx)
	result.AppConnections, _ = s.collectAppConnections(ctx, result.MaxConnections)

	return result, rows.Err()
}

func (s *Service) collectQueryHogs(ctx context.Context) ([]domain.QueryHog, error) {
	rows, err := s.postgres.QueryContext(ctx, `
		select
			pid,
			coalesce(datname, ''),
			coalesce(application_name, ''),
			coalesce(client_addr::text, ''),
			extract(epoch from now() - query_start),
			coalesce(query, ''),
			coalesce(state, '')
		from pg_stat_activity
		where state = 'active'
		order by query_start asc
		limit 5
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []domain.QueryHog
	for rows.Next() {
		var item domain.QueryHog
		var seconds float64
		if err := rows.Scan(&item.PID, &item.Database, &item.Application, &item.ClientAddr, &seconds, &item.Query, &item.State); err != nil {
			return nil, err
		}
		item.Duration = time.Duration(seconds * float64(time.Second))
		item.Query = trimQuery(item.Query)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) collectLeakCandidates(ctx context.Context) ([]domain.LeakCandidate, error) {
	rows, err := s.postgres.QueryContext(ctx, `
		select
			pid,
			coalesce(datname, ''),
			coalesce(application_name, ''),
			coalesce(client_addr::text, ''),
			extract(epoch from now() - state_change),
			coalesce(state, ''),
			coalesce(query, '')
		from pg_stat_activity
		where state in ('idle', 'idle in transaction')
		order by state_change asc
		limit 5
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []domain.LeakCandidate
	for rows.Next() {
		var item domain.LeakCandidate
		var seconds float64
		var state string
		if err := rows.Scan(&item.PID, &item.Database, &item.Application, &item.ClientAddr, &seconds, &state, &item.LastQuery); err != nil {
			return nil, err
		}
		item.IdleFor = time.Duration(seconds * float64(time.Second))
		item.InTxn = state == "idle in transaction"
		item.LastQuery = trimQuery(item.LastQuery)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) collectTopStatements(ctx context.Context) ([]domain.StatementMetrics, error) {
	rows, err := s.postgres.QueryContext(ctx, `
		select
			queryid::text,
			query,
			calls,
			mean_exec_time,
			total_exec_time
		from pg_stat_statements
		order by total_exec_time desc
		limit 5
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []domain.StatementMetrics
	for rows.Next() {
		var item domain.StatementMetrics
		var meanMs float64
		var totalMs float64
		if err := rows.Scan(&item.QueryID, &item.Query, &item.Calls, &meanMs, &totalMs); err != nil {
			return nil, err
		}
		item.Query = trimQuery(item.Query)
		item.MeanExecTime = time.Duration(meanMs * float64(time.Millisecond))
		item.TotalExecTime = time.Duration(totalMs * float64(time.Millisecond))
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) collectAppConnections(ctx context.Context, maxConnections int) ([]domain.AppConnectionStats, error) {
	rows, err := s.postgres.QueryContext(ctx, `
		select
			coalesce(nullif(application_name, ''), 'unknown'),
			coalesce(datname, ''),
			count(*),
			count(*) filter (where state = 'active'),
			count(*) filter (where state like 'idle in transaction%')
		from pg_stat_activity
		where pid <> pg_backend_pid() and backend_type = 'client backend'
		group by 1, 2
		order by 3 desc
		limit 20
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []domain.AppConnectionStats
	for rows.Next() {
		var item domain.AppConnectionStats
		if err := rows.Scan(&item.Application, &item.Database, &item.Total, &item.Active, &item.IdleInTxn); err != nil {
			return nil, err
		}
		if maxConnections > 0 {
			item.Share = float64(item.Total) / float64(maxConnections)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func queryInt(ctx context.Context, db *sql.DB, query string) (int, error) {
	var value int
	err := db.QueryRowContext(ctx, query).Scan(&value)
	return value, err
}

func trimQuery(query string) string {
	query = strings.Join(strings.Fields(query), " ")
	if len(query) > 240 {
		return query[:240]
	}
	return query
}
