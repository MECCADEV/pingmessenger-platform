ALTER TABLE users
  ADD COLUMN terms_accepted_version TEXT,
  ADD COLUMN terms_accepted_at TIMESTAMPTZ;
