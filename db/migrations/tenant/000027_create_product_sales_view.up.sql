CREATE MATERIALIZED VIEW daily_product_sales_stats
WITH (timescaledb.continuous) AS
SELECT 
    time_bucket('1 day', created_at) AS bucket,
    product_id,
    product_variant_id,
    COUNT(id) AS total_transaction,
    SUM(price) AS revenue
FROM transaction_item_ts
GROUP BY time_bucket('1 day', created_at), product_id, product_variant_id
WITH NO DATA;

SELECT add_retention_policy('daily_product_sales_stats', INTERVAL '1 year');

SELECT add_continuous_aggregate_policy(
  'daily_product_sales_stats',
  start_offset => INTERVAL '7 days',
  end_offset   => INTERVAL '10 minutes',
  schedule_interval => INTERVAL '3 hours'
);

CREATE MATERIALIZED VIEW monthly_product_sales_stats
WITH (timescaledb.continuous) AS
SELECT 
    time_bucket('1 month', bucket) AS bucket_month,
    product_id,
    product_variant_id,
    SUM(total_transaction) AS total_transaction,
    SUM(revenue) AS revenue
FROM daily_product_sales_stats
GROUP BY time_bucket('1 month', bucket), product_id, product_variant_id
WITH NO DATA;

SELECT add_continuous_aggregate_policy(
  'monthly_product_sales_stats',
  start_offset => INTERVAL '6 months',
  end_offset   => INTERVAL '10 minutes',
  schedule_interval => INTERVAL '1 day'
);
