CREATE TABLE email_message_ts (
  id BIGINT GENERATED ALWAYS AS IDENTITY,
  tenant_id VARCHAR NOT NULL,
  from_email VARCHAR NOT NULL,
  subject VARCHAR NOT NULL,
  email_date TIMESTAMP WITH TIME ZONE NOT NULL,
  parsed_context VARCHAR NOT NULL,
  parsed_data TEXT NOT NULL,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  PRIMARY KEY (id, created_at)
);

SELECT create_hypertable('email_message_ts', 'created_at');
SELECT add_retention_policy('email_message_ts', INTERVAL '3 days');
