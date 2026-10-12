-- Platform-owned payment ledger. PingPay-user only reports transaction state;
-- it does not own conversation visibility or client-facing history.
CREATE TYPE payment_type AS ENUM ('direct', 'fund_campaign');
CREATE TYPE payment_status AS ENUM ('pending', 'submitted', 'confirming', 'confirmed', 'failed', 'reverted', 'expired');

CREATE TABLE payments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  payment_id TEXT NOT NULL UNIQUE,
  conversation_id TEXT NOT NULL,
  from_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  to_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  payment_type payment_type NOT NULL,
  status payment_status NOT NULL DEFAULT 'pending',
  chain_id TEXT NOT NULL,
  transaction_id TEXT NOT NULL,
  block_id TEXT NOT NULL DEFAULT '',
  asset TEXT NOT NULL DEFAULT '',
  amount_atomic TEXT NOT NULL CHECK (amount_atomic ~ '^[0-9]+$'),
  version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_event_at TIMESTAMPTZ,
  CHECK (from_user_id <> to_user_id)
);
CREATE INDEX payments_conversation_recent ON payments(conversation_id, created_at DESC, id DESC);
CREATE INDEX payments_participant_recent ON payments(from_user_id, to_user_id, created_at DESC);
CREATE UNIQUE INDEX payments_chain_transaction_unique ON payments(chain_id, transaction_id);

CREATE TABLE payment_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id TEXT NOT NULL UNIQUE,
  payment_id UUID NOT NULL REFERENCES payments(id) ON DELETE CASCADE,
  status payment_status NOT NULL,
  version BIGINT NOT NULL CHECK (version > 0),
  payload JSONB NOT NULL,
  occurred_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX payment_events_payment_order ON payment_events(payment_id, version DESC);
