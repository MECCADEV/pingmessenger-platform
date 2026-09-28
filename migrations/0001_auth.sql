CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TYPE user_status AS ENUM ('pending_contact_verification', 'active', 'disabled', 'deleted');
CREATE TYPE contact_kind AS ENUM ('email', 'phone');
CREATE TYPE challenge_purpose AS ENUM ('signup_contact_verification', 'login', 'step_up');

CREATE TABLE users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  username CITEXT NOT NULL,
  password_hash TEXT NOT NULL,
  openim_user_id TEXT UNIQUE,
  status user_status NOT NULL DEFAULT 'pending_contact_verification',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX users_username_active_unique ON users (username) WHERE deleted_at IS NULL;

CREATE TABLE user_contacts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind contact_kind NOT NULL,
  value_normalized TEXT NOT NULL,
  verified_at TIMESTAMPTZ,
  is_primary BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (kind, value_normalized)
);
CREATE UNIQUE INDEX user_contacts_one_primary_per_kind ON user_contacts (user_id, kind) WHERE is_primary;

CREATE TABLE mfa_challenges (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID REFERENCES users(id) ON DELETE CASCADE,
  contact_id UUID NOT NULL REFERENCES user_contacts(id) ON DELETE CASCADE,
  purpose challenge_purpose NOT NULL,
  code_hash TEXT NOT NULL,
  attempts SMALLINT NOT NULL DEFAULT 0 CHECK (attempts >= 0 AND attempts <= 5),
  consumed_at TIMESTAMPTZ,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX mfa_challenges_pending_lookup ON mfa_challenges (contact_id, purpose, expires_at) WHERE consumed_at IS NULL;

CREATE TABLE recovery_codes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash TEXT NOT NULL UNIQUE,
  used_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE device_sessions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  refresh_token_hash TEXT NOT NULL UNIQUE,
  openim_session_id TEXT,
  platform_id TEXT NOT NULL,
  device_name TEXT,
  device_id TEXT,
  ip INET,
  user_agent TEXT,
  last_active_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  revoked_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX device_sessions_user_active ON device_sessions (user_id, last_active_at DESC) WHERE revoked_at IS NULL;

CREATE TABLE profile_assets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  storage_key TEXT NOT NULL UNIQUE,
  public_path TEXT NOT NULL,
  content_type TEXT NOT NULL,
  byte_size BIGINT NOT NULL CHECK (byte_size > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX profile_assets_one_active_per_user ON profile_assets (user_id) WHERE deleted_at IS NULL;
