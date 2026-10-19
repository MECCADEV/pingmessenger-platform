-- Persist the reason for platform-generated terminal transitions so a
-- websocket connected to another API replica can reconstruct the same event.
ALTER TABLE payments ADD COLUMN IF NOT EXISTS update_reason TEXT NOT NULL DEFAULT '';
