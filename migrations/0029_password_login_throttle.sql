-- Failed password login counters are shared by every API replica.
CREATE TABLE password_login_failures (
  scope TEXT NOT NULL CHECK (scope IN ('account', 'ip')),
  key TEXT NOT NULL,
  window_started_at TIMESTAMPTZ NOT NULL,
  failure_count SMALLINT NOT NULL CHECK (failure_count > 0),
  locked_until TIMESTAMPTZ,
  PRIMARY KEY (scope, key)
);

CREATE INDEX password_login_failures_window_idx
  ON password_login_failures (window_started_at);

CREATE INDEX password_login_failures_locked_until_idx
  ON password_login_failures (locked_until)
  WHERE locked_until IS NOT NULL;
