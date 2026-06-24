CREATE TABLE platform_statistics (
  date DATE NOT NULL,
  type VARCHAR(7) NOT NULL,
  platform VARCHAR(50) NOT NULL,
  transaction_count BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  PRIMARY KEY (date, type, platform),
  CONSTRAINT platform_statistics_type_chk CHECK (type IN ('daily', 'monthly'))
) WITH (fillfactor=90);

CREATE INDEX pls_daily_date_idx ON platform_statistics (date) WHERE type = 'daily';
CREATE INDEX pls_monthly_date_idx ON platform_statistics (date) WHERE type = 'monthly';

CREATE TRIGGER platform_statistics_set_updated_at
BEFORE UPDATE ON platform_statistics
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
