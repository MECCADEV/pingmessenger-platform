CREATE TABLE discovered_contacts (
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  contact_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, contact_user_id),
  CHECK (user_id <> contact_user_id)
);

CREATE INDEX discovered_contacts_contact_idx ON discovered_contacts (contact_user_id);
