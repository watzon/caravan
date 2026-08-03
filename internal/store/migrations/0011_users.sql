-- Accounts and roles (SPEC §11, RBAC track). Caravan's password stopped being
-- a property of the server and became a property of a person: an admin runs
-- the box, a member asks it for things.
--
-- The open default survives untouched. Zero rows in this table means exactly
-- what an unset `password_hash` meant before — no login at all, which is the
-- right shape for a box on a trusted LAN — so a household that never creates a
-- user notices nothing.
CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    -- Usernames are compared case-insensitively so "Chris" and "chris" cannot
    -- both exist: two accounts that look identical in a list are an
    -- impersonation waiting to happen. NOCASE sits on the column, so the
    -- implicit UNIQUE index inherits it and lookups get it for free.
    username      TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    -- An argon2id PHC string, never a password. Like the setting it replaces,
    -- it is the one column that never leaves the process (SPEC §12).
    password_hash TEXT    NOT NULL,
    role          TEXT    NOT NULL CHECK (role IN ('admin', 'member')),
    created_at    TEXT    NOT NULL,
    updated_at    TEXT    NOT NULL
);

-- Fold the legacy single password into an admin account, in SQL, so nobody is
-- locked out by an upgrade: the password that opened the server yesterday
-- opens it today under the username 'admin'. A server that had no password set
-- matches no row here and stays open, which is the same promise from the other
-- direction.
--
-- The timestamps are written in the store's own format (see formatTime):
-- RFC3339 in UTC, which is what parseTime reads back.
INSERT INTO users (username, password_hash, role, created_at, updated_at)
SELECT 'admin', value, 'admin',
       strftime('%Y-%m-%dT%H:%M:%SZ', 'now'),
       strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
FROM settings
WHERE key = 'password_hash' AND value != '';

-- The setting is gone rather than merely ignored: leaving a stale hash in the
-- settings table would leave a second, unreachable credential in the database.
DELETE FROM settings WHERE key = 'password_hash';

-- Who asked. Zero is deliberately not a foreign key and deliberately not NULL:
-- it is the honest answer for a row that predates accounts, and for one made
-- while the server runs open with no users at all. Deleting a person must not
-- delete the history of what they asked for.
ALTER TABLE requests ADD COLUMN requested_by INTEGER NOT NULL DEFAULT 0;
