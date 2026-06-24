CREATE MATERIALIZED VIEW daily_expense_stats
WITH (timescaledb.continuous) AS
SELECT 
    time_bucket('1 day', created_at) AS bucket,
    type,
    subject_id,
    COUNT(id) AS total_expense_count,
    SUM(amount) AS total_expense_amount
FROM expense
GROUP BY time_bucket('1 day', created_at), type, subject_id
WITH NO DATA;

SELECT add_retention_policy('daily_expense_stats', INTERVAL '1 year');

SELECT add_continuous_aggregate_policy(
  'daily_expense_stats',
  start_offset => INTERVAL '7 days',
  end_offset   => INTERVAL '10 minutes',
  schedule_interval => INTERVAL '3 hours'
);

CREATE MATERIALIZED VIEW monthly_expense_stats
WITH (timescaledb.continuous) AS
SELECT 
    time_bucket('1 month', bucket) AS bucket_month,
    type,
    subject_id,
    SUM(total_expense_count) AS total_expense_count,
    SUM(total_expense_amount) AS total_expense_amount
FROM daily_expense_stats
GROUP BY time_bucket('1 month', bucket), type, subject_id
WITH NO DATA;

SELECT add_continuous_aggregate_policy(
  'monthly_expense_stats',
  start_offset => INTERVAL '6 months',
  end_offset   => INTERVAL '10 minutes',
  schedule_interval => INTERVAL '1 day'
);
