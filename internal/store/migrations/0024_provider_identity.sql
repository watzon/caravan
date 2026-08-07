-- Generalized provider identity, and provider fallback chains.
--
-- 0013 is the precedent and this is its generalization. stash_id was the adult
-- module's parallel identity for `series`: a TEXT column whose "unmatched"
-- value is '' and whose matched rows are unique, under a partial index. That
-- shape was right and the only thing wrong with it was the name — one column
-- per provider does not scale past two providers.
--
-- (provider, provider_ref) is the same idea with the provider named rather
-- than implied. tmdb_id and stash_id STAY: they are what discover decoration,
-- the requests table and every NFO uniqueid still key on. provider_ref is the
-- identity NEW providers key on, and the rung the upserts match on FIRST — a
-- ref is exact, a tmdb_id is a compatibility alias for one provider's refs.
ALTER TABLE movies ADD COLUMN provider     TEXT NOT NULL DEFAULT '';
ALTER TABLE movies ADD COLUMN provider_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE series ADD COLUMN provider     TEXT NOT NULL DEFAULT '';
ALTER TABLE series ADD COLUMN provider_ref TEXT NOT NULL DEFAULT '';

-- Backfill. The kind guard on the TMDB pass is not decoration: an adult series
-- with a stray tmdb_id and no stash id would otherwise be pinned permanently
-- to a provider that has never heard of it — nothing after this pass would
-- ever claim it back. (A row with a stash id is safe either way, because the
-- stash-box pass below overwrites; the guard is for the row it cannot reach.)
UPDATE movies SET provider = 'tmdb', provider_ref = CAST(tmdb_id AS TEXT)
    WHERE tmdb_id > 0;
UPDATE series SET provider = 'tmdb', provider_ref = CAST(tmdb_id AS TEXT)
    WHERE tmdb_id > 0 AND kind != 'adult';
UPDATE series SET provider = 'stashbox', provider_ref = stash_id
    WHERE kind = 'adult' AND stash_id != '';

-- 0001's treatment of tmdb_id and 0013's of stash_id, once, for every
-- provider: matched rows are unique per (provider, id), and any number of
-- unmatched rows may coexist — which is what a scan that found files before it
-- found metadata produces.
CREATE UNIQUE INDEX idx_movies_provider_ref ON movies (provider, provider_ref)
    WHERE provider_ref != '';
CREATE UNIQUE INDEX idx_series_provider_ref ON series (provider, provider_ref)
    WHERE provider_ref != '';

-- Fallback chains. `providers` is the ordered list a library identifies new
-- items through; `provider` survives as its head, kept in sync by the store,
-- so every reader written against 0022 keeps answering exactly as before.
-- Provider ids are validated against core.Providers() before they are ever
-- written, so the string concat below cannot produce invalid JSON.
ALTER TABLE libraries ADD COLUMN providers TEXT NOT NULL DEFAULT '';
UPDATE libraries SET providers = '["' || provider || '"]' WHERE provider != '';
