-- Executed once by the postgres:16 entrypoint on first boot.
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

-- Least-privileged monitoring role used by PoolWatch.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'poolwatch') THEN
        CREATE ROLE poolwatch WITH LOGIN PASSWORD 'poolwatch';
    END IF;
END
$$;

GRANT pg_monitor TO poolwatch;
GRANT CONNECT ON DATABASE postgres TO poolwatch;
