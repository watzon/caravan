-- Stash-box endpoints become rows.
--
-- "stash-box" is a protocol, not a service: StashDB, FansDB, PMV-Stash and
-- ThePornDB are separate catalogues speaking it, each with its own account and
-- its own UUIDs. One endpoint in `settings` could only ever describe one of
-- them, so an owner who wanted two had to choose. This table is the plural of
-- that setting, on the `indexers` pattern — a credentialed remote is a row, not
-- a key-value pair.
--
-- `provider_id` IS the string stored in `series.provider` and in a library's
-- `providers` chain. It is not a display name and it is not derivable from one:
-- renaming an instance must never re-point the rows pinned to it. 'stashbox' —
-- the bare id — is the pre-instance endpoint, kept as a first-class instance so
-- that no adult row already on disk has to be rewritten. Instances minted from
-- here on are 'stashbox:<slug>' (see core.ValidProviderInstanceID).
--
-- `endpoint` is always an absolute URL. The old setting admitted '' as "the
-- TPDB preset", and that sentinel cannot survive the plural: with several rows
-- it would let two instances be silently the same box, each holding a different
-- account's key and each claiming the other's UUIDs. The preset lives in the
-- UI's picker, where a default belongs; what is stored is what was chosen.
--
-- There is deliberately no UNIQUE on `endpoint`. Two accounts on one box is a
-- legitimate configuration — a personal key and a shared one — and the identity
-- that must not collide is the provider id, which is unique below.
CREATE TABLE stashbox_instances (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id TEXT    NOT NULL UNIQUE,
    name        TEXT    NOT NULL UNIQUE,
    endpoint    TEXT    NOT NULL,
    api_key     TEXT    NOT NULL DEFAULT '',
    created_at  TEXT    NOT NULL,
    updated_at  TEXT    NOT NULL
);

-- Carry the settings pair in as the legacy instance.
--
-- The WHERE is what keeps an install that never touched the adult module free
-- of a row describing a box it has never talked to: the same promise-of-absence
-- 0013 makes by not seeding the Adult library. A stashbox_api_key row exists
-- only because someone submitted the credential form, and adult_enabled='true'
-- covers the enable flow that wrote the switch.
--
-- The name is a guess at what the owner would call it, and only two guesses are
-- honest: the endpoint that was the preset is ThePornDB, and any other endpoint
-- is a box whose name only its owner knows. Both are editable afterwards; the
-- provider id is not.
INSERT INTO stashbox_instances (provider_id, name, endpoint, api_key, created_at, updated_at)
SELECT
    'stashbox',
    CASE
        WHEN COALESCE((SELECT value FROM settings WHERE key = 'stashbox_endpoint'), '')
             IN ('', 'https://theporndb.net/graphql') THEN 'ThePornDB'
        ELSE 'Stash-box'
    END,
    CASE
        WHEN COALESCE((SELECT value FROM settings WHERE key = 'stashbox_endpoint'), '') = ''
             THEN 'https://theporndb.net/graphql'
        ELSE (SELECT value FROM settings WHERE key = 'stashbox_endpoint')
    END,
    COALESCE((SELECT value FROM settings WHERE key = 'stashbox_api_key'), ''),
    strftime('%Y-%m-%dT%H:%M:%SZ', 'now'),
    strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
WHERE EXISTS (SELECT 1 FROM settings WHERE key = 'stashbox_api_key')
   OR EXISTS (SELECT 1 FROM settings WHERE key = 'adult_enabled' AND value = 'true');

-- The keys are gone, not merely unread. A credential left behind in a table the
-- settings screen still lists is a second copy of a secret nothing keeps in
-- step with the first, and the endpoint beside it would go on disagreeing with
-- the instance the moment either is edited.
DELETE FROM settings WHERE key IN ('stashbox_endpoint', 'stashbox_api_key');

-- ---------------------------------------------------------------------------
-- The bare stash_id indexes, demoted.
-- ---------------------------------------------------------------------------
--
-- 0013 made stash_id globally unique because there was one box and its UUIDs
-- were therefore unambiguous. With several, they are not: the public boxes are
-- forks of one another and mint identical UUIDs for the same site. Two
-- instances legitimately holding the same UUID is the plural's whole point, and
-- a global unique index turns it into a failed insert — the second box's site
-- simply cannot be added.
--
-- Identity has not been given up, it has moved: 0024's
-- idx_series_provider_ref is UNIQUE on (provider, provider_ref), and every
-- matched adult row carries both (normalizeSeriesProvider fills them at the
-- write door). The same site on two boxes is two rows, which is the truth —
-- they are two catalogue entries with two sets of scenes.
DROP INDEX idx_series_stash_id;
CREATE INDEX idx_series_stash_id ON series (stash_id) WHERE stash_id != '';

-- Episodes keep a uniqueness, narrowed to the scope where it is still true. A
-- scene belongs to one site, so one series may not hold the same scene twice —
-- that is the constraint the import path actually relies on. Across series it
-- was never a real constraint, only an artefact of there being one box.
DROP INDEX idx_episodes_stash_id;
CREATE UNIQUE INDEX idx_episodes_stash_id ON episodes (series_id, stash_id)
    WHERE stash_id != '';

-- idx_requests_pending_scene is deliberately left bare. A request is a wish
-- for a scene, made from a discover screen, and it carries no provider column
-- to key on: giving it one is a change to the requests data model rather than
-- to an index. The accepted limit is that two boxes minting the same scene UUID
-- share one pending row, which merges two wishes for what is almost always the
-- same scene — a wrong merge here costs a duplicate request, not a wrong file.
