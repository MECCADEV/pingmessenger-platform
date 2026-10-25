CREATE TABLE notification_global_preferences (
  user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  enabled BOOLEAN NOT NULL DEFAULT true,
  mode TEXT NOT NULL DEFAULT 'all' CHECK (mode IN ('all', 'mentions_only', 'none')),
  sound TEXT NOT NULL DEFAULT 'default' CHECK (sound IN ('default', 'silent')),
  max_update_frequency_seconds INTEGER NOT NULL DEFAULT 2 CHECK (max_update_frequency_seconds IN (2, 5, 10)),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE notification_conversation_preferences (
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  conversation_id TEXT NOT NULL CHECK (length(conversation_id) BETWEEN 1 AND 256),
  enabled BOOLEAN,
  mode TEXT CHECK (mode IN ('all', 'mentions_only', 'none')),
  sound TEXT CHECK (sound IN ('default', 'silent')),
  max_update_frequency_seconds INTEGER CHECK (max_update_frequency_seconds IN (2, 5, 10)),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, conversation_id)
);

COMMENT ON TABLE notification_global_preferences IS
  'Effective account-wide chat notification defaults; absent rows use enabled/all/default/2s.';
COMMENT ON TABLE notification_conversation_preferences IS
  'Per-conversation overrides; NULL fields inherit the corresponding account-wide value.';
