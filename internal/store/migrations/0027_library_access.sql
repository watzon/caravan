-- Library `active` toggle and per-library user access.
--
-- The adult module's two switches become properties of a library. `active` is
-- `adult_enabled` generalized: a master switch that binds EVERY caller,
-- including an admin, and deletes nothing. `restricted` is `users.adult_access`
-- generalized: a library that answers only to the accounts named in
-- `library_access`, plus admins. Both defaults describe the state every library
-- already lives in today — on, and open to every account — so an upgraded
-- install behaves identically until somebody opens the Libraries screen.
--
-- This migration READS `settings.adult_enabled` and `users.adult_access` and
-- removes NEITHER. That is not tidiness deferred, it is a constraint: an old
-- backup re-migrates from its own schema version on every open, so this file
-- must be able to read those two columns forever. Retiring them is 0028's job,
-- and folding the two migrations together would make this file unable to run
-- against the backups it exists to upgrade.

-- ---------------------------------------------------------------------------
-- libraries.active and libraries.restricted
-- ---------------------------------------------------------------------------
--
-- `active` is the master switch, generalized from the adult module's. Nobody
-- bypasses it — an admin who deactivates a library has hidden it from
-- themselves, which is the whole point of having a switch rather than a
-- permission — and it deletes nothing: the rows, the files and the grants all
-- survive an off, exactly as SetAdultEnabled has always promised.
--
-- `restricted` is the per-account grant, generalized. 0 is "every account", 1
-- is "the allow-list in library_access, plus admins". It is a flag of its own
-- rather than an inference from an empty roster because "restricted to nobody
-- yet" and "open to everybody" are different answers, and a library that opened
-- itself the moment its last grantee was removed would be the opposite of what
-- the owner asked for.
--
-- Both defaults are the state every existing library is already in.
ALTER TABLE libraries ADD COLUMN active     INTEGER NOT NULL DEFAULT 1;
ALTER TABLE libraries ADD COLUMN restricted INTEGER NOT NULL DEFAULT 0;

-- ---------------------------------------------------------------------------
-- library_access — the allow-list.
-- ---------------------------------------------------------------------------
--
-- The `library_indexers` shape: a composite primary key, cascades both ways,
-- and an index on the column the other direction is asked about (a session asks
-- "which libraries may I see", which reads by user_id).
--
-- A grant is a live permission, not a record of one. When the account or the
-- library goes, so does the row — a permission outliving the thing it named is
-- a grant waiting to be handed to whoever next lands on that id.
CREATE TABLE library_access (
    library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users(id)     ON DELETE CASCADE,
    PRIMARY KEY (library_id, user_id)
);

CREATE INDEX idx_library_access_user ON library_access (user_id);

-- ---------------------------------------------------------------------------
-- The backfill.
-- ---------------------------------------------------------------------------
--
-- Every statement below keys on `kind = 'adult'` and never on a provider id.
-- Provider ids became instance-qualified in 0026 ('stashbox', 'stashbox:<slug>'
-- and whatever the owner has minted since), so a backfill that asked which box
-- a library talks to would answer for some adult libraries and not others. The
-- kind is what the module ever actually meant.

-- An adult library is restricted not because of what it holds, but because of
-- what it already did: it was reachable only by an account somebody granted.
-- Carrying that forward as a per-library flag changes no user's access.
UPDATE libraries SET restricted = 1 WHERE kind = 'adult';

-- The module switch, read into SQL. store.AdultEnabled's own rule is that
-- absent means off and so does anything that will not parse, and this mirrors
-- it: only the two spellings strconv.ParseBool accepts as true in the form the
-- writer produces ('true', and '1' for a hand-edited row) leave the library
-- active. Anything else — absent, 'false', a typo — is off, which is the only
-- safe reading of a switch whose job is to keep a module absent.
UPDATE libraries SET active = 0
WHERE kind = 'adult'
  AND COALESCE((SELECT value FROM settings WHERE key = 'adult_enabled'), 'false')
      NOT IN ('true', '1');

-- The grants. Every account holding `adult_access` gets a row on every adult
-- library, which is exactly the access it had.
--
-- There is deliberately no role filter. 0013 stores an admin's flag as 0 so
-- that a demotion to member does not silently carry access along with it, and
-- copying the column verbatim reproduces that rule for free: a demoted admin
-- arrives here ungranted, just as they would have before.
INSERT OR IGNORE INTO library_access (library_id, user_id)
SELECT l.id, u.id
FROM libraries l
JOIN users u
WHERE l.kind = 'adult' AND u.adult_access = 1;
