-- Optional, case-insensitive usernames and immutable public Ping IDs.
ALTER TABLE users ALTER COLUMN username DROP NOT NULL;

CREATE SEQUENCE IF NOT EXISTS ping_id_seq
  MINVALUE 1
  MAXVALUE 999999999999
  START WITH 1;

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS ping_id CHAR(12);

ALTER TABLE users
  ALTER COLUMN ping_id SET DEFAULT lpad(nextval('ping_id_seq')::text, 12, '0');

UPDATE users
SET ping_id = lpad(nextval('ping_id_seq')::text, 12, '0')
WHERE ping_id IS NULL;

ALTER TABLE users ALTER COLUMN ping_id SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS users_ping_id_unique ON users (ping_id);

-- CITEXT already provides case-insensitive uniqueness; this partial index makes
-- the nullable/deleted-account behavior explicit for future schema readers.
DROP INDEX IF EXISTS users_username_active_unique;
CREATE UNIQUE INDEX users_username_active_unique ON users (username) WHERE username IS NOT NULL AND deleted_at IS NULL;
