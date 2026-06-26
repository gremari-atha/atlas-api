CREATE TABLE tenant (
    id VARCHAR PRIMARY KEY NOT NULL,
    secret VARCHAR NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL
);

CREATE TABLE task_queue (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    execute_at TIMESTAMP WITH TIME ZONE NOT NULL,
    subject_id VARCHAR NOT NULL,
    context VARCHAR NOT NULL,
    payload VARCHAR NOT NULL,
    status VARCHAR,
    error_message VARCHAR,
    tenant_id VARCHAR NOT NULL REFERENCES tenant(id) ON DELETE CASCADE ON UPDATE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
    attempt INTEGER DEFAULT 0
);

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

CREATE TABLE email_forward_queue (
    id BIGINT GENERATED ALWAYS AS IDENTITY,
    payload_json TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    attempt INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
    started_at TIMESTAMP WITH TIME ZONE,
    last_error TEXT,
    PRIMARY KEY (id)
);

CREATE INDEX idx_email_forward_queue_status_available 
ON email_forward_queue (status, available_at, created_at);
