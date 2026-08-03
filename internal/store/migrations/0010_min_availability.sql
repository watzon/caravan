-- Movies gate their automatic search on a release stage, Radarr's "minimum
-- availability": don't look for a movie that cannot exist as a file yet.
-- Announced searches immediately, in_cinemas waits for the theatrical date,
-- released (the default) waits for a home release. The gate itself lives in
-- internal/wanted; these columns are the user's choice and the provider dates
-- the choice is judged against.
ALTER TABLE movies ADD COLUMN min_availability TEXT NOT NULL DEFAULT 'released'
    CHECK (min_availability IN ('announced', 'in_cinemas', 'released'));

-- The home-release dates from the provider (TMDB release_dates types 4 and 5),
-- refreshed whenever movie metadata is. release_date (0001) stays the
-- theatrical date. Empty means the provider has not published one yet.
ALTER TABLE movies ADD COLUMN digital_release  TEXT NOT NULL DEFAULT '';
ALTER TABLE movies ADD COLUMN physical_release TEXT NOT NULL DEFAULT '';

-- A movie request can carry the asker's availability choice into the approve.
-- Empty means unspecified — every series request, and a movie request that
-- left the default alone.
ALTER TABLE requests ADD COLUMN min_availability TEXT NOT NULL DEFAULT ''
    CHECK (min_availability IN ('', 'announced', 'in_cinemas', 'released'));
