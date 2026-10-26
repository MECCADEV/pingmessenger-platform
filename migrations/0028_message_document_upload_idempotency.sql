-- Keep the client upload key with the document tombstone so replay cannot
-- resurrect media after explicit deletion or expiry.
CREATE TABLE message_document_upload_idempotency (
  owner_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  idempotency_key TEXT NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
  request_hash CHAR(64) NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
  document_id UUID NOT NULL UNIQUE REFERENCES message_documents(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (owner_id, idempotency_key)
);
