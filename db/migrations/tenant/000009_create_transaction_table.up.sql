CREATE TABLE transaction (
  id VARCHAR PRIMARY KEY NOT NULL,
  customer VARCHAR NOT NULL,
  platform VARCHAR NOT NULL,
  total_price INTEGER NOT NULL,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL
);

CREATE INDEX transactions_created_at_idx ON transaction (created_at);
CREATE INDEX transactions_platform_idx ON transaction (platform);
CREATE INDEX transactions_platform_created_at_idx ON transaction (platform, created_at);
