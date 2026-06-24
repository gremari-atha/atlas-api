SELECT remove_retention_policy('syslog_ts', if_exists => true);
DROP TABLE IF EXISTS syslog_ts CASCADE;
