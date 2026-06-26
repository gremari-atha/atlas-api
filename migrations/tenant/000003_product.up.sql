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
  cooldown INT NOT NULL DEFAULT 0,
  max_user INT NOT NULL DEFAULT 5,
  base_price INT NOT NULL DEFAULT 0,
  product_id BIGINT NOT NULL REFERENCES product(id) ON DELETE RESTRICT ON UPDATE CASCADE,
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
  sku VARCHAR NOT NULL,
  price BIGINT NOT NULL,
  variant VARCHAR,
  product_id BIGINT NOT NULL REFERENCES product(id) ON DELETE RESTRICT ON UPDATE CASCADE,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  UNIQUE (product_id, platform)
) WITH (fillfactor=90);

CREATE TRIGGER platform_product_set_updated_at
BEFORE UPDATE ON platform_product
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

CREATE INDEX idx_platform_product_product_id ON platform_product (product_id);
