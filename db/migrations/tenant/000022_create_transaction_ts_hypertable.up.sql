CREATE TABLE transaction_ts (
  id VARCHAR NOT NULL,
  customer VARCHAR NOT NULL,
  platform VARCHAR NOT NULL,
  total_price INTEGER NOT NULL,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  PRIMARY KEY (id, created_at)
);

SELECT create_hypertable('transaction_ts', 'created_at');
SELECT add_retention_policy('transaction_ts', INTERVAL '1 year');

CREATE INDEX idx_transaction_platform_time ON transaction_ts (platform, created_at DESC);
