ALTER TABLE users
  ADD COLUMN mood VARCHAR(100),
  ADD COLUMN is_admin BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE moderation_reports (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  reporter_user_id UUID NOT NULL REFERENCES users(id),
  target_user_id UUID NOT NULL REFERENCES users(id),
  conversation_id TEXT,
  message_id TEXT,
  message_seq BIGINT,
  reason TEXT NOT NULL CHECK (reason IN ('spam', 'abuse', 'harassment', 'inappropriate', 'other')),
  details TEXT CHECK (details IS NULL OR char_length(details) <= 2000),
  message_snapshot JSONB,
  status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'actioned', 'dismissed')),
  resolution_notes TEXT CHECK (resolution_notes IS NULL OR char_length(resolution_notes) <= 2000),
  notification_status TEXT NOT NULL DEFAULT 'pending' CHECK (notification_status IN ('pending', 'sent', 'failed')),
  notification_error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  reviewed_at TIMESTAMPTZ,
  reviewed_by UUID REFERENCES users(id),
  CHECK ((message_id IS NULL AND message_seq IS NULL AND conversation_id IS NULL AND message_snapshot IS NULL)
      OR (message_id IS NOT NULL AND message_seq > 0 AND conversation_id IS NOT NULL AND message_snapshot IS NOT NULL)),
  CHECK (reporter_user_id <> target_user_id)
);

CREATE UNIQUE INDEX moderation_reports_message_dedup
  ON moderation_reports (reporter_user_id, target_user_id, conversation_id, message_id, reason)
  WHERE message_id IS NOT NULL;
CREATE UNIQUE INDEX moderation_reports_user_dedup
  ON moderation_reports (reporter_user_id, target_user_id, reason)
  WHERE message_id IS NULL;
CREATE INDEX moderation_reports_queue_idx
  ON moderation_reports (status, created_at DESC);
CREATE INDEX moderation_reports_target_idx
  ON moderation_reports (target_user_id, created_at DESC);

CREATE TABLE password_reset_tokens (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX password_reset_tokens_pending_idx
  ON password_reset_tokens (user_id, expires_at)
  WHERE consumed_at IS NULL;
