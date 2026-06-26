CREATE TABLE expense (
  id BIGSERIAL NOT NULL,
  subject_id BIGINT,
  type VARCHAR(50) NOT NULL DEFAULT 'global',
  amount BIGINT NOT NULL,
  note VARCHAR,
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
  PRIMARY KEY (id, created_at)
);

SELECT create_hypertable('expense', 'created_at');
SELECT add_retention_policy('expense', INTERVAL '1 year');

-- Legacy Indexes
CREATE INDEX idx_expense_polymorphic ON expense (type, subject_id, created_at DESC) WHERE subject_id IS NOT NULL;
CREATE INDEX idx_expense_global_time ON expense (created_at DESC) WHERE type = 'global' AND subject_id IS NULL;

-- New performance index
CREATE INDEX idx_expense_type_subject ON expense (type, subject_id);
