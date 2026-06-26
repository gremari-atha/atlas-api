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

CREATE TABLE transaction_item_ts (
  id BIGSERIAL NOT NULL,
  transaction_id VARCHAR NOT NULL,
  price BIGINT NOT NULL,
  account_id BIGINT,
  account_user_id BIGINT,
  product_id BIGINT,
  product_variant_id BIGINT,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  PRIMARY KEY (id, created_at)
);

SELECT create_hypertable('transaction_item_ts', 'created_at');
SELECT add_retention_policy('transaction_item_ts', INTERVAL '1 year');

-- Legacy Indexes
CREATE INDEX idx_item_transaction_id ON transaction_item_ts (transaction_id, created_at DESC);
CREATE INDEX idx_item_account_time ON transaction_item_ts (account_id, created_at DESC) WHERE account_id IS NOT NULL;
CREATE INDEX idx_item_product_variant_time ON transaction_item_ts (product_id, product_variant_id, created_at DESC) WHERE product_id IS NOT NULL;

-- New performance indexes
CREATE INDEX idx_transaction_item_ts_transaction_id ON transaction_item_ts (transaction_id);
CREATE INDEX idx_transaction_item_ts_account_id ON transaction_item_ts (account_id);
