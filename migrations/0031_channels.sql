CREATE TABLE channels (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  openim_group_id TEXT NOT NULL UNIQUE CHECK (openim_group_id ~ '^ch_.+'),
  created_by UUID NOT NULL REFERENCES users(id),
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
  description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 2000),
  photo_url TEXT NOT NULL DEFAULT '' CHECK (length(photo_url) <= 2048),
  visibility TEXT NOT NULL CHECK (visibility IN ('public', 'private')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX channels_public_search_idx ON channels (created_at DESC, id DESC)
  WHERE visibility = 'public';
CREATE INDEX channels_public_name_idx ON channels (lower(name))
  WHERE visibility = 'public';

CREATE TABLE channel_pinned_posts (
  channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  openim_message_id TEXT NOT NULL CHECK (length(openim_message_id) BETWEEN 1 AND 128),
  pinned_by UUID NOT NULL REFERENCES users(id),
  pinned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (channel_id, openim_message_id)
);

CREATE INDEX channel_pinned_posts_order_idx ON channel_pinned_posts (channel_id, pinned_at DESC, openim_message_id);
