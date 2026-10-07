CREATE TABLE device_keys (
  session_id UUID PRIMARY KEY REFERENCES device_sessions(id) ON DELETE CASCADE,
  public_jwk JSONB NOT NULL,
  fingerprint TEXT NOT NULL UNIQUE,
  registered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  rotated_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ
);

CREATE TABLE device_proof_challenges (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id UUID NOT NULL REFERENCES device_sessions(id) ON DELETE CASCADE,
  nonce_hash TEXT NOT NULL,
  audience TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ,
  attempts SMALLINT NOT NULL DEFAULT 0 CHECK (attempts >= 0 AND attempts <= 5),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX device_proof_challenges_pending ON device_proof_challenges (session_id, expires_at) WHERE consumed_at IS NULL;

CREATE TABLE openim_device_tokens (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id UUID NOT NULL REFERENCES device_sessions(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  token_ciphertext TEXT NOT NULL,
  platform_id INTEGER NOT NULL CHECK (platform_id BETWEEN 1 AND 12),
  expires_at TIMESTAMPTZ NOT NULL,
  issued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX openim_device_tokens_one_active_per_session ON openim_device_tokens (session_id) WHERE revoked_at IS NULL;
