-- Durable MFA challenge throttles are shared by all API replicas.
CREATE TABLE mfa_challenge_rate_limits (
  scope TEXT NOT NULL CHECK (scope IN ('contact', 'ip')),
  key TEXT NOT NULL,
  window_started_at TIMESTAMPTZ NOT NULL,
  last_issued_at TIMESTAMPTZ NOT NULL,
  issued_count SMALLINT NOT NULL CHECK (issued_count > 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (scope, key)
);

CREATE INDEX mfa_challenge_rate_limits_updated_idx
  ON mfa_challenge_rate_limits (updated_at);
