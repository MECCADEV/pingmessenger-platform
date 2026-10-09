-- Keep the default generator ahead of any IDs populated during backfill or
-- restored from a database snapshot.
SELECT setval(
  'ping_id_seq',
  GREATEST(1, COALESCE((SELECT MAX(ping_id::bigint) FROM users), 0)),
  true
);
