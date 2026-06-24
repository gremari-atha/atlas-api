SELECT remove_retention_policy('transaction_ts', if_exists => true);
DROP TABLE IF EXISTS transaction_ts CASCADE;
