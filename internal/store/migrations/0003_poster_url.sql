-- Provider poster URLs (SPEC §5.1 Library): artwork for items that have no
-- file on disk yet. An added-but-undownloaded movie has no folder, so it can
-- have no poster.jpg; the provider's own URL fills the gap until the
-- organizer writes a local poster, which always wins.
ALTER TABLE movies ADD COLUMN poster_url TEXT NOT NULL DEFAULT '';
ALTER TABLE series ADD COLUMN poster_url TEXT NOT NULL DEFAULT '';
