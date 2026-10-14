CREATE TABLE message_documents (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  storage_key TEXT NOT NULL UNIQUE,
  file_name TEXT NOT NULL,
  content_type TEXT NOT NULL,
  byte_size BIGINT NOT NULL CHECK (byte_size > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  deleted_at TIMESTAMPTZ,
  s3_deleted_at TIMESTAMPTZ,
  delete_attempts INTEGER NOT NULL DEFAULT 0,
  last_delete_error TEXT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX message_documents_owner_active ON message_documents (owner_id, created_at DESC)
  WHERE deleted_at IS NULL;
CREATE INDEX message_documents_cleanup ON message_documents (deleted_at)
  WHERE deleted_at IS NOT NULL AND s3_deleted_at IS NULL;
CREATE INDEX message_documents_expiry ON message_documents (expires_at)
  WHERE deleted_at IS NULL;
