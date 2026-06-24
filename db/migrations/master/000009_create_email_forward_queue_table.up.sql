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
