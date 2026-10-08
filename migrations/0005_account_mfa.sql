-- Account MFA is independent from the optional contact used at signup. A user
-- may enroll multiple factors; exactly one successful enabled factor completes
-- a pending password login.
CREATE TYPE mfa_factor_kind AS ENUM ('email', 'phone', 'totp');

CREATE TABLE user_mfa_factors (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind mfa_factor_kind NOT NULL,
  contact_id UUID REFERENCES user_contacts(id) ON DELETE CASCADE,
  secret_ciphertext TEXT,
  label TEXT NOT NULL DEFAULT '',
  enabled_at TIMESTAMPTZ,
  disabled_at TIMESTAMPTZ,
  preferred BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((kind IN ('email','phone') AND contact_id IS NOT NULL AND secret_ciphertext IS NULL) OR
         (kind = 'totp' AND contact_id IS NULL AND secret_ciphertext IS NOT NULL))
);
CREATE UNIQUE INDEX user_mfa_factors_contact_active_unique
  ON user_mfa_factors(user_id, kind, contact_id)
  WHERE disabled_at IS NULL AND contact_id IS NOT NULL;
CREATE UNIQUE INDEX user_mfa_factors_totp_active_unique
  ON user_mfa_factors(user_id, kind)
  WHERE disabled_at IS NULL AND kind = 'totp';
CREATE UNIQUE INDEX user_mfa_factors_one_preferred
  ON user_mfa_factors(user_id) WHERE preferred AND disabled_at IS NULL;

CREATE TABLE pending_login_challenges (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  factor_id UUID NOT NULL REFERENCES user_mfa_factors(id) ON DELETE CASCADE,
  platform_id TEXT NOT NULL,
  device_name TEXT,
  device_id TEXT,
  code_hash TEXT,
  attempts SMALLINT NOT NULL DEFAULT 0 CHECK (attempts >= 0 AND attempts <= 5),
  consumed_at TIMESTAMPTZ,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX pending_login_challenges_active_lookup
  ON pending_login_challenges(user_id, expires_at) WHERE consumed_at IS NULL;

CREATE TABLE mfa_enrollment_challenges (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  factor_id UUID NOT NULL REFERENCES user_mfa_factors(id) ON DELETE CASCADE,
  code_hash TEXT,
  attempts SMALLINT NOT NULL DEFAULT 0 CHECK (attempts >= 0 AND attempts <= 5),
  consumed_at TIMESTAMPTZ,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX mfa_enrollment_challenges_active_lookup
  ON mfa_enrollment_challenges(user_id, expires_at) WHERE consumed_at IS NULL;
