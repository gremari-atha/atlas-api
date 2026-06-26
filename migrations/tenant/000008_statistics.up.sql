-- Account Stats
CREATE MATERIALIZED VIEW daily_account_stats
WITH (timescaledb.continuous) AS
SELECT 
    time_bucket('1 day', created_at) AS bucket,
    account_id,
    COUNT(id) AS total_transaction,
    SUM(price) AS revenue
FROM transaction_item_ts
GROUP BY time_bucket('1 day', created_at), account_id
WITH NO DATA;

SELECT add_retention_policy('daily_account_stats', INTERVAL '1 year');

SELECT add_continuous_aggregate_policy(
  'daily_account_stats',
  start_offset => INTERVAL '7 days',
  end_offset   => INTERVAL '10 minutes',
  schedule_interval => INTERVAL '3 hours'
);

CREATE MATERIALIZED VIEW monthly_account_stats
WITH (timescaledb.continuous) AS
SELECT 
    time_bucket('1 month', bucket) AS bucket_month,
    account_id,
    SUM(total_transaction) AS total_transaction,
    SUM(revenue) AS revenue
FROM daily_account_stats
GROUP BY time_bucket('1 month', bucket), account_id
WITH NO DATA;

SELECT add_continuous_aggregate_policy(
  'monthly_account_stats',
  start_offset => INTERVAL '6 months',
  end_offset   => INTERVAL '10 minutes',
  schedule_interval => INTERVAL '1 day'
);

-- Platform Stats
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

-- Product Sales Stats
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

-- Peak Hour Stats
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

-- Expense Stats
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
