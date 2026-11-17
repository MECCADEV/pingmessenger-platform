CREATE TABLE channel_post_likes (
  channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  openim_message_id TEXT NOT NULL CHECK (length(openim_message_id) BETWEEN 1 AND 128),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (channel_id, openim_message_id, user_id)
);

CREATE INDEX channel_post_likes_count_idx ON channel_post_likes(channel_id, openim_message_id);
