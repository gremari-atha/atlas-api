SELECT remove_retention_policy('expense', if_exists => true);
DROP TABLE IF EXISTS expense CASCADE;
