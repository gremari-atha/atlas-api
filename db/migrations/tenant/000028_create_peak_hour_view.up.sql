CREATE MATERIALIZED VIEW peak_hour_stats
WITH (timescaledb.continuous) AS
SELECT 
    time_bucket('1 hour', created_at) AS bucket,
    COUNT(id) AS total_transaction
FROM transaction_ts
GROUP BY time_bucket('1 hour', created_at)
WITH NO DATA;

SELECT add_retention_policy('peak_hour_stats', INTERVAL '5 years');

SELECT add_continuous_aggregate_policy(
  'peak_hour_stats',
  start_offset => INTERVAL '3 days',
  end_offset   => INTERVAL '10 minutes',
  schedule_interval => INTERVAL '3 hours'
);
