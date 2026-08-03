-- Libraries stop being implications and become rows (PLAN phase 8).
--
-- Until now "Movies" and "TV" were two constants in internal/library and two
-- folders under the storage root. That is enough right up to the moment
-- somebody wants a different indexer set, different categories, a different
-- download client or a different default profile for one of them — which is
-- the whole reason people run a second Sonarr beside their first. A row per
-- library absorbs that into one instance.
--
-- `kind` is UNIQUE because today there is exactly one library per kind, and
-- that is what maps an item to its library: every row in `movies` belongs to
-- the movie library, every row in `series` to the tv library. No media table
-- gains a library_id here — phase 9 gives `series` a kind discriminator and
-- the mapping keeps working through it.
CREATE TABLE libraries (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    kind               TEXT    NOT NULL UNIQUE CHECK (kind IN ('movie', 'tv')),
    name               TEXT    NOT NULL,
    -- Storage-root-relative with forward slashes, like every other stored path
    -- (SPEC §1.2 pillar 3). These are the paths the scanner and the organizer
    -- already build; the row records the layout, it does not redefine it.
    root_path          TEXT    NOT NULL,
    dlna_visible       INTEGER NOT NULL DEFAULT 1,
    -- NULL is "no override, use the global setting". These columns are
    -- nullable rather than defaulted because "" is a meaningful routing value
    -- (nothing configured) and must stay distinguishable from "not answered
    -- here".
    route_torrent      TEXT,
    route_usenet       TEXT,
    -- NULL again, with the same reading: an item that names no profile falls
    -- through to the library's default, and a library that names none falls
    -- through to the store's (ResolveQualityProfile). Soft, like every
    -- quality_profile_id: deleting a profile can never orphan a library.
    quality_profile_id INTEGER
);

-- Seeded from the layout that was already on disk, so an upgraded install
-- describes exactly what it was already doing: the same two folders, both
-- visible over DLNA, every overridable setting inherited from the globals.
-- Nothing observable changes until a user edits one of these rows.
INSERT INTO libraries (kind, name, root_path) VALUES
    ('movie', 'Movies', 'library/Movies'),
    ('tv',    'Series', 'library/TV');

-- Per (library, indexer) search configuration: whether this library searches
-- that indexer at all, and which categories it sends when it does.
--
-- The table is deliberately sparse. An ABSENT row means enabled with the
-- indexer's own categories — today's behavior — so this migration seeds
-- nothing and a newly added indexer is searched by every library without
-- anyone writing a row. Only a deviation from the default costs storage.
CREATE TABLE library_indexers (
    library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    indexer_id INTEGER NOT NULL REFERENCES indexers(id) ON DELETE CASCADE,
    enabled    INTEGER NOT NULL DEFAULT 1,
    -- JSON array of ints replacing `indexers.categories` for this pair. NULL
    -- is "the indexer's own categories"; an empty array is an override to
    -- "search this indexer unfiltered", which is what an empty category list
    -- has always meant.
    categories TEXT,
    PRIMARY KEY (library_id, indexer_id)
);
