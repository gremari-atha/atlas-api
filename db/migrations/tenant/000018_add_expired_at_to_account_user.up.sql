ALTER TABLE account_user ADD COLUMN expired_at TIMESTAMP WITH TIME ZONE;

UPDATE account_user AS au
SET expired_at = a.batch_end_date
FROM account AS a
WHERE au.account_id = a.id
AND a.batch_end_date IS NOT NULL;
