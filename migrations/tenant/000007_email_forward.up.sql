CREATE TABLE email_subject (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  context VARCHAR NOT NULL,
  subject VARCHAR NOT NULL,
  extract_method VARCHAR NOT NULL,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL
) WITH (fillfactor=90);

CREATE TRIGGER email_subject_set_updated_at
BEFORE UPDATE ON email_subject
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

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
