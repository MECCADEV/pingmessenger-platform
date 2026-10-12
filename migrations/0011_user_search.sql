-- Accelerate case-insensitive partial username directory searches without
-- changing the case-insensitive uniqueness contract on users.username.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS users_username_search_trgm_active
  ON users USING gin ((username::text) gin_trgm_ops)
  WHERE username IS NOT NULL AND deleted_at IS NULL AND status = 'active';
