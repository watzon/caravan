-- Phase 6: `download_clients` gets the columns an external client actually
-- needs (SPEC §5.1, §7; PLAN phase 6 task 1).
--
-- 0001 sketched this table before any external client existed, with a
-- host/port pair. Every other remote Caravan talks to — indexers, Jellyfin —
-- is configured as one base URL instead, because that is the only shape that
-- covers https, a non-default port and a reverse-proxy path prefix without a
-- column per variation. Same reconciliation 0002 did for `releases`, `grabs`
-- and `downloads`.
--
-- This URL is the ONE place an absolute foreign address is configuration
-- rather than a bug: it names a machine, not a path in the library. The paths
-- these clients report live outside the storage root and stay in the download
-- state; nothing the library owns (`media_files`) ever sees them.
ALTER TABLE download_clients DROP COLUMN host;
ALTER TABLE download_clients DROP COLUMN port;

-- The base URL of the client's web API, e.g. http://127.0.0.1:8080.
ALTER TABLE download_clients ADD COLUMN url TEXT NOT NULL DEFAULT '';

-- The second credential shape. qBittorrent and NZBGet authenticate with the
-- username/password already on this table; SABnzbd authenticates with an API
-- key. A row uses one shape or the other, decided by `kind`, so both columns
-- are nullable-by-default rather than a polymorphic blob.
--
-- Both this and `password` are credentials: SPEC §12 keeps them out of logs,
-- out of caravan.yaml and out of API responses.
ALTER TABLE download_clients ADD COLUMN api_key TEXT NOT NULL DEFAULT '';

-- Lowest number wins when more than one enabled client can take a release,
-- matching `indexers.priority`. Which client *can* take it is decided by the
-- protocol its kind speaks, not by a column: qbittorrent is torrent, sabnzbd
-- and nzbget are usenet, and that mapping belongs with the code that speaks
-- each protocol.
ALTER TABLE download_clients ADD COLUMN priority INTEGER NOT NULL DEFAULT 25;
