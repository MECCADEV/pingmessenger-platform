-- Nickname is presentation metadata, intentionally not unique and independent
-- from the immutable, case-insensitively unique username.
ALTER TABLE users ADD COLUMN IF NOT EXISTS nickname VARCHAR(128) NOT NULL DEFAULT '';
