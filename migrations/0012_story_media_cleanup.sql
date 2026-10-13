-- Keep soft-deleted story rows for durable delete events while recording when
-- their S3 object has been removed. The expiry worker retries rows where this
-- marker is still NULL after transient object-store failures.
ALTER TABLE stories ADD COLUMN IF NOT EXISTS media_deleted_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS stories_media_cleanup
  ON stories (deleted_at)
  WHERE deleted_at IS NOT NULL AND media_deleted_at IS NULL;
