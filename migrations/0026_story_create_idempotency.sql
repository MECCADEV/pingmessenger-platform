-- Retain keyed story-create results independently from story-row retention so
-- a deleted or expired story cannot be recreated by replaying the same key.
CREATE TABLE story_create_idempotency (
  owner_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  idempotency_key TEXT NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
  request_hash CHAR(64) NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
  story_id UUID REFERENCES stories(id) ON DELETE SET NULL,
  retain_until TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (owner_id, idempotency_key)
);

CREATE INDEX story_create_idempotency_retention ON story_create_idempotency (retain_until);
