-- Keep explicit story-view attempts replayable without changing legacy callers.
-- The ledger survives story deletion/expiry so an ambiguous retry cannot create
-- a fresh view or overwrite a later reaction after the original story expires.
CREATE TABLE story_view_idempotency (
  viewer_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  idempotency_key TEXT NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
  story_id UUID REFERENCES stories(id) ON DELETE SET NULL,
  request_hash CHAR(64) NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
  viewed_at TIMESTAMPTZ NOT NULL,
  reaction TEXT NOT NULL,
  retain_until TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (viewer_id, idempotency_key)
);

CREATE INDEX story_view_idempotency_retention ON story_view_idempotency (retain_until);
