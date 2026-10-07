-- OpenIM can produce the same JWT string for a same-second device reissue.
-- Token history must therefore permit a revoked row and its replacement to
-- share a digest; only one non-revoked token per session is authoritative.
ALTER TABLE openim_device_tokens DROP CONSTRAINT IF EXISTS openim_device_tokens_token_hash_key;
