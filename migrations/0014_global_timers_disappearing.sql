-- Global, operator-controlled timers. Timer values are read by the platform
-- and cached briefly; clients may read them but cannot mutate them.
CREATE TABLE global_timers (
  timer_key        TEXT PRIMARY KEY,
  duration_seconds INTEGER NOT NULL CHECK (duration_seconds >= 0),
  enabled          BOOLEAN NOT NULL DEFAULT TRUE,
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO global_timers(timer_key, duration_seconds)
VALUES
  ('message_edit_window', 900),
  ('stale_payment_failed_after', 180),
  ('disappearing_message_default', 86400)
ON CONFLICT (timer_key) DO NOTHING;

-- A user may choose a per-conversation retention duration. The global timer
-- supplies the default; 0 disables disappearing messages for this user/chat.
CREATE TABLE conversation_disappearing_settings (
  conversation_id TEXT NOT NULL,
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  duration_seconds INTEGER NOT NULL CHECK (duration_seconds >= 0 AND duration_seconds <= 2592000),
  enabled         BOOLEAN NOT NULL DEFAULT TRUE,
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (conversation_id, user_id)
);
