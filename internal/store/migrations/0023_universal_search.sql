-- Universal indexer search (plan: multi-library media roots, part B).
--
-- A Prowlarr-style free-text search can grab a release that is tied to no
-- movie or series at all — only to a LIBRARY the user chose at grab time.
-- Three columns carry that through the pipeline:
--
--   * `grabs.library_id`: the library an untied grab's payload belongs to.
--     Nullable, NULL for every historical row: a grab tied to a movie or a
--     series never needed it (and now carries it as well, so history and the
--     queue can gate adult visibility on one uniform field).
--   * `unmatched_files.library_id`: where a finished untied download parks.
--     The scan-review screen scopes its manual match to this library.
--   * `releases.categories`: the indexer's own filing for a cached result, a
--     JSON array of ints. core.Release used to say categories are not cached
--     because nothing after the match needed them; the untied grab path does —
--     it must answer "is this release adult" for a caller without the adult
--     grant WITHOUT re-searching, or a cached adult release id would be
--     grabbable by anyone who learned it.
ALTER TABLE grabs ADD COLUMN library_id INTEGER;
ALTER TABLE unmatched_files ADD COLUMN library_id INTEGER;
ALTER TABLE releases ADD COLUMN categories TEXT NOT NULL DEFAULT '';
