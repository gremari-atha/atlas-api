SELECT remove_retention_policy('transaction_item_ts', if_exists => true);
DROP TABLE IF EXISTS transaction_item_ts CASCADE;

SELECT remove_retention_policy('transaction_ts', if_exists => true);
DROP TABLE IF EXISTS transaction_ts CASCADE;
