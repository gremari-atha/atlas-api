CREATE TABLE product_sales_statistics (
  date DATE NOT NULL,
  type VARCHAR(7) NOT NULL,
  product_variant_id BIGINT NOT NULL,
  items_sold BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  PRIMARY KEY (date, type, product_variant_id),
  CONSTRAINT product_sales_statistics_type_chk CHECK (type IN ('daily', 'monthly'))
) WITH (fillfactor=90);

CREATE INDEX pss_daily_date_idx ON product_sales_statistics (date) WHERE type = 'daily';
CREATE INDEX pss_monthly_date_idx ON product_sales_statistics (date) WHERE type = 'monthly';

CREATE TRIGGER product_sales_statistics_set_updated_at
BEFORE UPDATE ON product_sales_statistics
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
