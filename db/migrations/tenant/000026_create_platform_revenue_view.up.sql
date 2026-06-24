CREATE MATERIALIZED VIEW daily_platform_stats
WITH (timescaledb.continuous) AS
SELECT 
    time_bucket('1 day', created_at) AS bucket,
    platform,
    COUNT(id) AS total_transaction,
    SUM(total_price) AS revenue
FROM transaction_ts
GROUP BY time_bucket('1 day', created_at), platform
WITH NO DATA;

SELECT add_retention_policy('daily_platform_stats', INTERVAL '1 year');

SELECT add_continuous_aggregate_policy(
  'daily_platform_stats',
  start_offset => INTERVAL '7 days',
  end_offset   => INTERVAL '10 minutes',
  schedule_interval => INTERVAL '3 hours'
);

CREATE MATERIALIZED VIEW monthly_platform_stats
WITH (timescaledb.continuous) AS
SELECT 
    time_bucket('1 month', bucket) AS bucket_month,
    platform,
    SUM(total_transaction) AS total_transaction,
    SUM(revenue) AS revenue
FROM daily_platform_stats
GROUP BY time_bucket('1 month', bucket), platform
WITH NO DATA;

SELECT add_continuous_aggregate_policy(
  'monthly_platform_stats',
  start_offset => INTERVAL '6 months',
  end_offset   => INTERVAL '10 minutes',
  schedule_interval => INTERVAL '1 day'
);
