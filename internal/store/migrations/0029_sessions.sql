-- Live logins (SPEC §11). The session cookie is an opaque handle; this table
-- is the pairing of that handle to an account, so a restart does not sign
-- everyone out. The cookie value itself is never stored: token_hash is
-- SHA-256 of the cookie, so a copied database is not a copied login.
--
-- Sessions are children of users. Deleting an account must not leave a
-- browser signed in as a person who no longer exists, which is why the
-- foreign key cascades — unlike requests.requested_by, which is history
-- and is deliberately not a foreign key.
CREATE TABLE sessions (
    token_hash TEXT    NOT NULL PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TEXT    NOT NULL
);

CREATE INDEX idx_sessions_user ON sessions(user_id);
