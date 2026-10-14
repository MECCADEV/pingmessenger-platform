-- Normalized on-chain observations. A payment remains an application intent;
-- these tables hold facts extracted from the chain RPC and are idempotent by
-- (chain_id, transaction_id).
CREATE TABLE chain_transactions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  chain_id TEXT NOT NULL,
  transaction_id TEXT NOT NULL,
  block_id TEXT NOT NULL DEFAULT '',
  slot BIGINT,
  status TEXT NOT NULL CHECK (status IN ('pending', 'confirmed', 'failed')),
  fee_atomic TEXT NOT NULL DEFAULT '' CHECK (fee_atomic = '' OR fee_atomic ~ '^[0-9]+$'),
  fee_payer TEXT NOT NULL DEFAULT '',
  error_code TEXT NOT NULL DEFAULT '',
  raw_payload JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (chain_id, transaction_id)
);
CREATE INDEX chain_transactions_updated ON chain_transactions(updated_at DESC, id DESC);

ALTER TABLE payments ADD COLUMN chain_transaction_id UUID REFERENCES chain_transactions(id) ON DELETE SET NULL;
CREATE INDEX payments_chain_transaction ON payments(chain_transaction_id);

CREATE TABLE chain_transfers (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  chain_transaction_id UUID NOT NULL REFERENCES chain_transactions(id) ON DELETE CASCADE,
  transfer_index INTEGER NOT NULL CHECK (transfer_index >= 0),
  transfer_type TEXT NOT NULL CHECK (transfer_type IN ('native', 'token')),
  asset TEXT NOT NULL DEFAULT '',
  mint TEXT NOT NULL DEFAULT '',
  from_address TEXT NOT NULL DEFAULT '',
  to_address TEXT NOT NULL DEFAULT '',
  amount_atomic TEXT NOT NULL CHECK (amount_atomic ~ '^[0-9]+$'),
  decimals INTEGER NOT NULL DEFAULT 0 CHECK (decimals >= 0 AND decimals <= 255),
  instruction_index INTEGER NOT NULL DEFAULT -1,
  inner_instruction_index INTEGER NOT NULL DEFAULT -1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (chain_transaction_id, transfer_index)
);
CREATE INDEX chain_transfers_transaction_order ON chain_transfers(chain_transaction_id, transfer_index);
CREATE INDEX chain_transfers_parties ON chain_transfers(from_address, to_address);
