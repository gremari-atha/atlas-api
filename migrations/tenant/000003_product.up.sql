CREATE TABLE product (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name VARCHAR UNIQUE NOT NULL,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL
) WITH (fillfactor=90);

CREATE TRIGGER product_set_updated_at
BEFORE UPDATE ON product
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

CREATE TABLE product_variant (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name VARCHAR NOT NULL,
  duration BIGINT NOT NULL,
  interval BIGINT NOT NULL,
  cooldown BIGINT NOT NULL,
  copy_template TEXT,
  base_price BIGINT NOT NULL DEFAULT 0,
  product_id BIGINT NOT NULL REFERENCES product(id) ON DELETE CASCADE ON UPDATE CASCADE,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  UNIQUE (product_id, name)
) WITH (fillfactor=90);

CREATE TRIGGER product_variant_set_updated_at
BEFORE UPDATE ON product_variant
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

CREATE INDEX idx_product_variant_product_id ON product_variant (product_id);

CREATE TABLE platform_product (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  platform VARCHAR NOT NULL,
  name VARCHAR NOT NULL,
  variant VARCHAR,
  product_variant_id BIGINT NOT NULL REFERENCES product_variant(id) ON DELETE CASCADE ON UPDATE CASCADE,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL
) WITH (fillfactor=90);

CREATE TRIGGER platform_product_set_updated_at
BEFORE UPDATE ON platform_product
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

CREATE INDEX idx_platform_product_product_variant_id ON platform_product (product_variant_id);
