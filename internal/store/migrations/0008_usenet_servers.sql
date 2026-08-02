-- Phase 7: `usenet_servers`, the article sources the embedded Usenet engine
-- fetches from (SPEC §5.1, §7; PLAN phase 7 task 2).
--
-- This is deliberately NOT a row in `download_clients`. A download client is a
-- program Caravan hands an NZB to and then asks about progress; a news server
-- is where the embedded engine reads article bodies from itself. They have
-- different protocols, different credentials, different failure modes, and a
-- user who runs the built-in engine configures servers here while `sabnzbd`
-- stays empty. Folding them into one table would mean a `kind` column whose
-- value decides which half of the columns mean anything, which is the shape
-- 0007 already had to unpick once.
--
-- Every column here maps one-to-one onto internal/usenet/nntp.ServerConfig,
-- which is the struct the engine dials with. That package holds no database
-- knowledge, so this table is the only place the mapping lives.
CREATE TABLE usenet_servers (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    -- The user's label, unique so the settings screen can 409 a duplicate
    -- rather than surfacing a constraint failure as a 500. It is also what
    -- names the server in engine errors, which is why it never has to be a
    -- hostname.
    name            TEXT NOT NULL UNIQUE,
    host            TEXT NOT NULL,
    -- 0 means the protocol default (563 with TLS, 119 without). The API
    -- resolves it before writing, so a stored row always names a real port,
    -- but the default is kept here so a hand-edited row still dials.
    port            INTEGER NOT NULL DEFAULT 0,
    -- Implicit TLS (NNTPS), on by default: every provider that matters offers
    -- 563, and the credential below crosses this socket. There is no STARTTLS
    -- column because there is no STARTTLS support — an upgrade that can be
    -- stripped is worse than none.
    tls             INTEGER NOT NULL DEFAULT 1,
    -- AUTHINFO credentials. An empty username means the server is used
    -- anonymously and no AUTHINFO is sent.
    --
    -- `password` is a credential: it lives here, never in caravan.yaml, never
    -- in a log line, and never in an API response (SPEC §12). The API reports
    -- only whether one is stored.
    username        TEXT NOT NULL DEFAULT '',
    password        TEXT NOT NULL DEFAULT '',
    -- The per-server connection cap, 0 for the engine default. It is a hard
    -- cap and not a target: exceeding a provider's limit does not slow the
    -- download down, it gets connections refused for everyone on the account.
    max_connections INTEGER NOT NULL DEFAULT 0,
    -- Lowest wins, matching `indexers.priority` and `download_clients
    -- .priority`. Higher numbers are backup servers: they are only asked for
    -- the articles the servers above them did not have.
    priority        INTEGER NOT NULL DEFAULT 25,
    -- A disabled server keeps its configuration and is skipped when the engine
    -- builds its pool.
    enabled         INTEGER NOT NULL DEFAULT 1,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
