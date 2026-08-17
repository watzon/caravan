-- +goose Up

-- Durable URL slugs for first-class libraries. The SPA addresses a shelf as
-- /l/{slug} so a second movie library and an anime series stop sharing the
-- kind-shaped /movies and /series roots. The slug is minted once and kept
-- across a rename, so a bookmark survives a label edit.
--
-- Default shelves of each kind take the names the old paths already taught
-- people: movies, series, anime, adult. Every other row gets lib-{id}, which
-- is unique and legal; CreateLibrary then mints a name-based slug for rows
-- created after this migration.

ALTER TABLE libraries ADD COLUMN slug TEXT NOT NULL DEFAULT '';

UPDATE libraries SET slug = CASE
    WHEN kind = 'movie' AND is_default = 1 THEN 'movies'
    WHEN kind = 'tv' AND is_default = 1 THEN 'series'
    WHEN kind = 'anime' AND is_default = 1 THEN 'anime'
    WHEN kind = 'adult' AND is_default = 1 THEN 'adult'
    ELSE 'lib-' || id
END
WHERE slug = '';

CREATE UNIQUE INDEX idx_libraries_slug ON libraries (slug);
