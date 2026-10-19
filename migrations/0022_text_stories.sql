-- Text statuses do not have an object-store payload. Keep image/video rows
-- compatible while allowing a validated text body and background color.
ALTER TABLE stories ALTER COLUMN storage_key DROP NOT NULL;
ALTER TABLE stories ALTER COLUMN media_url SET DEFAULT '';
ALTER TABLE stories ALTER COLUMN content_type SET DEFAULT '';
ALTER TABLE stories DROP CONSTRAINT IF EXISTS stories_byte_size_check;
ALTER TABLE stories ADD CONSTRAINT stories_byte_size_check CHECK (byte_size >= 0 AND byte_size <= 10485760);
ALTER TABLE stories ADD COLUMN IF NOT EXISTS text_content TEXT NOT NULL DEFAULT '';
ALTER TABLE stories ADD COLUMN IF NOT EXISTS background_color CHAR(7);
ALTER TABLE stories ADD CONSTRAINT stories_text_payload_check CHECK (
  (media_type = 'text' AND text_content <> '' AND background_color IS NOT NULL AND storage_key IS NULL)
  OR
  (media_type IN ('image', 'video') AND storage_key IS NOT NULL AND text_content = '' AND background_color IS NULL)
);
