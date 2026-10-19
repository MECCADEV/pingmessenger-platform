-- Deletion tombstones retain only immutable identity values. They prevent a
-- deleted account's username, PingID, email, or phone from being reassigned.
CREATE TABLE account_identity_reservations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('username', 'ping_id', 'email', 'phone')),
  value CITEXT NOT NULL,
  reserved_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (kind, value)
);
CREATE INDEX account_identity_reservations_user ON account_identity_reservations(user_id);

-- The existing partial username index allowed a deleted tombstone to be
-- replaced. Username uniqueness is now lifetime-scoped, like PingID.
DROP INDEX IF EXISTS users_username_active_unique;
CREATE UNIQUE INDEX users_username_lifetime_unique ON users (username) WHERE username IS NOT NULL;
