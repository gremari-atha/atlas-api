SELECT remove_retention_policy('email_message_ts', if_exists => true);
DROP TABLE IF EXISTS email_message_ts CASCADE;
