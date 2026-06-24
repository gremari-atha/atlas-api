CREATE TABLE peak_hour_statistics (
  date DATE NOT NULL,
  type VARCHAR(7) NOT NULL,
  hour SMALLINT NOT NULL,
  transaction_count BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  PRIMARY KEY (date, type, hour),
  CONSTRAINT peak_hour_statistics_type_chk CHECK (type IN ('daily', 'monthly')),
  CONSTRAINT peak_hour_statistics_hour_chk CHECK (hour BETWEEN 0 AND 23)
) WITH (fillfactor=90);

CREATE INDEX phs_daily_date_idx ON peak_hour_statistics (date) WHERE type = 'daily';
CREATE INDEX phs_monthly_date_idx ON peak_hour_statistics (date) WHERE type = 'monthly';

CREATE TRIGGER peak_hour_statistics_set_updated_at
BEFORE UPDATE ON peak_hour_statistics
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
