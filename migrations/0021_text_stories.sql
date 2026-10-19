-- The enum value is intentionally isolated in its own migration transaction.
-- PostgreSQL does not allow a newly-added enum label to be referenced by a
-- constraint until the ALTER TYPE transaction has committed.
ALTER TYPE story_media_type ADD VALUE IF NOT EXISTS 'text';
