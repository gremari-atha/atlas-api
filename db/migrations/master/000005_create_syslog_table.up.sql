CREATE TABLE syslog (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    level VARCHAR NOT NULL,
    context VARCHAR NOT NULL,
    message TEXT NOT NULL,
    stack TEXT,
    tenant_id VARCHAR REFERENCES tenant(id) ON DELETE CASCADE ON UPDATE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL
);

CREATE INDEX idx_logs_tenant_level_time ON syslog (tenant_id, level, created_at DESC);
CREATE INDEX idx_logs_tenant_context_time ON syslog (tenant_id, context, created_at DESC);
CREATE INDEX idx_logs_created_at ON syslog (created_at);
