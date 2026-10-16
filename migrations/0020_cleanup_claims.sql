-- Cleanup workers claim rows for a bounded lease so multiple API replicas do
-- not concurrently revoke/delete the same object. A stale lease is recoverable
-- after five minutes when a worker crashes.
ALTER TABLE message_documents ADD COLUMN IF NOT EXISTS cleanup_claimed_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS message_documents_cleanup_claim ON message_documents (deleted_at, cleanup_claimed_at)
  WHERE deleted_at IS NOT NULL AND s3_deleted_at IS NULL;

ALTER TABLE stories ADD COLUMN IF NOT EXISTS media_cleanup_claimed_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS stories_media_cleanup_claim ON stories (deleted_at, media_cleanup_claimed_at)
  WHERE deleted_at IS NOT NULL AND media_deleted_at IS NULL;
