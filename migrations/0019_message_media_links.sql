-- Links a platform-owned document to its OpenIM message coordinates. The
-- cleanup worker revokes the message first, then removes the S3 object.
CREATE TABLE message_media_links (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  document_id UUID NOT NULL REFERENCES message_documents(id) ON DELETE CASCADE,
  owner_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  conversation_id TEXT NOT NULL,
  seq BIGINT NOT NULL CHECK (seq > 0),
  openim_user_id TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  cleanup_claimed_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ,
  s3_deleted_at TIMESTAMPTZ,
  revoke_attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(document_id, conversation_id, seq)
);
CREATE INDEX message_media_links_cleanup ON message_media_links(expires_at, cleanup_claimed_at)
  WHERE revoked_at IS NULL OR s3_deleted_at IS NULL;
