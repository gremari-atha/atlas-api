CREATE TABLE revenue_statistics (
  date DATE NOT NULL,
  type VARCHAR(7) NOT NULL,
  total_revenue BIGINT NOT NULL DEFAULT 0,
  transaction_count BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  PRIMARY KEY (date, type),
  CONSTRAINT revenue_statistics_type_chk CHECK (type IN ('daily', 'monthly'))
) WITH (fillfactor=90);

CREATE INDEX revenue_statistics_daily_date_idx ON revenue_statistics (date) WHERE type = 'daily';
CREATE INDEX revenue_statistics_monthly_date_idx ON revenue_statistics (date) WHERE type = 'monthly';

CREATE TRIGGER revenue_statistics_set_updated_at
BEFORE UPDATE ON revenue_statistics
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
