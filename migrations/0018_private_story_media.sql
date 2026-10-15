-- Story media is private. The storage_key is the authoritative identifier;
-- media_url is retained for compatibility with existing scans/clients but is
-- never populated with a public URL by the application.
UPDATE stories SET media_url = '' WHERE media_url <> '';
COMMENT ON COLUMN stories.media_url IS 'Deprecated compatibility field; runtime responses contain short-lived signed URLs derived from storage_key';
