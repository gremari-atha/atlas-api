CREATE TABLE syslog_ts (
    id BIGINT GENERATED ALWAYS AS IDENTITY,
    level VARCHAR NOT NULL,
    context VARCHAR NOT NULL,
    message TEXT NOT NULL,
    stack TEXT,
    tenant_id VARCHAR,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
    PRIMARY KEY (id, created_at)
);

SELECT create_hypertable('syslog_ts', 'created_at');
SELECT add_retention_policy('syslog_ts', INTERVAL '7 days');

CREATE INDEX idx_logs_ts_tenant_level_time ON syslog_ts (tenant_id, level, created_at DESC);
CREATE INDEX idx_logs_ts_tenant_context_time ON syslog_ts (tenant_id, context, created_at DESC);
