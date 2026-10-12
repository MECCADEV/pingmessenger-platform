CREATE TYPE story_visibility AS ENUM ('contacts', 'allow', 'exclude');
CREATE TYPE story_media_type AS ENUM ('image', 'video');

CREATE TABLE stories (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  media_type story_media_type NOT NULL,
  storage_key TEXT NOT NULL UNIQUE,
  media_url TEXT NOT NULL,
  content_type TEXT NOT NULL,
  byte_size BIGINT NOT NULL CHECK (byte_size > 0 AND byte_size <= 10485760),
  caption TEXT NOT NULL DEFAULT '',
  visibility story_visibility NOT NULL DEFAULT 'contacts',
  expires_at TIMESTAMPTZ NOT NULL,
  deleted_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX stories_owner_active ON stories (owner_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX stories_expiry ON stories (expires_at) WHERE deleted_at IS NULL;

CREATE TABLE story_visibility_users (
  story_id UUID NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY (story_id, user_id)
);

CREATE TABLE story_recipients (
  story_id UUID NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
  recipient_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY (story_id, recipient_id)
);
CREATE INDEX story_recipients_recipient ON story_recipients (recipient_id, story_id);

CREATE TABLE story_events (
  event_id BIGSERIAL PRIMARY KEY,
  story_id UUID NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
  owner_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  recipient_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  event_type TEXT NOT NULL CHECK (event_type IN ('created', 'deleted')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX story_events_recipient_cursor ON story_events (recipient_id, event_id);

CREATE TABLE story_views (
  story_id UUID NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
  viewer_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  viewed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  reaction TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (story_id, viewer_id)
);

-- scope_key is "global" or the owner UUID. The row stores the greatest
-- acknowledged event ID for that scope and is intentionally monotonic.
CREATE TABLE story_sync_cursors (
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  scope_key TEXT NOT NULL,
  cursor BIGINT NOT NULL CHECK (cursor >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, scope_key)
);
