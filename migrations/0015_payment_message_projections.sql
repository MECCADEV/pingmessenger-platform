-- OpenIM is a projection of the payment ledger. Keep message IDs for retry,
-- audit, and reconciliation without making message delivery part of payment
-- authorization.
CREATE TABLE payment_message_projections (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  payment_id      UUID NOT NULL REFERENCES payments(id) ON DELETE CASCADE,
  kind            TEXT NOT NULL CHECK (kind IN ('payment.created', 'payment.updated')),
  version         BIGINT NOT NULL CHECK (version > 0),
  server_msg_id   TEXT NOT NULL,
  client_msg_id   TEXT NOT NULL,
  send_time_ms    BIGINT NOT NULL DEFAULT 0,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(payment_id, kind, version),
  UNIQUE(server_msg_id)
);
CREATE INDEX payment_message_projections_payment ON payment_message_projections(payment_id, version DESC);
