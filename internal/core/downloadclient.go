package core

import "time"

// External download client types (SPEC §5.1, §7 `download_clients`). These are
// the values stored in `download_clients.kind`.
//
// The embedded engine is deliberately not one of them: it has no host, no
// credentials and nothing to test, so it is configured through the engine
// settings instead of through a row here.
const (
	DownloadClientQBittorrent = "qbittorrent"
	DownloadClientSABnzbd     = "sabnzbd"
	DownloadClientNZBGet      = "nzbget"
)

// DownloadClientConfig is one configured external download client.
//
// Caravan ships with none (SPEC §12): every entry is something the user added,
// and with none configured the embedded engine keeps handling torrents exactly
// as it did before.
//
// URL is the one absolute foreign address in Caravan's configuration. It names
// a machine, not a path in the library, so it does not break the root-relative
// rule that governs every stored path (SPEC §1.2 pillar 3). The download
// directories these clients report are foreign absolute paths too; they belong
// to the download state, never to `media_files`.
type DownloadClientConfig struct {
	ID int64
	// Type is one of the DownloadClient* constants.
	Type string
	// Name is the user-facing label, unique across clients.
	Name string
	// URL is the base URL of the client's web API, e.g.
	// "http://127.0.0.1:8080", optionally with a reverse-proxy path prefix.
	URL string
	// Username and Password authenticate qBittorrent and NZBGet.
	Username string
	// Password is a credential: it lives in the database, never in the
	// bootstrap YAML, never in logs, and never in an API response (SPEC §12).
	Password string
	// APIKey authenticates SABnzbd. Same credential rules as Password.
	APIKey string
	// Category is the client-side label grabs are filed under, so a user who
	// sorts their download client by category can still find Caravan's work.
	// Empty means the client's own default.
	Category string
	// Priority breaks ties when more than one enabled client can take a
	// release: lowest wins, matching indexers.
	Priority int
	// Enabled excludes the client from routing when false, without losing its
	// configuration.
	Enabled bool
}

// DownloadClientHealth is one external client the queue poller cannot reach
// (SPEC §5.1, PLAN phase 6 task 4).
//
// Only unhealthy clients are ever reported: a healthy client is the normal
// case and needs no banner. Error is the poll failure the user is shown — the
// same class of message the client's test button already surfaces, and like
// that one it never quotes a credential (SPEC §12).
type DownloadClientHealth struct {
	// ID is the `download_clients.id` of the client.
	ID int64
	// Name is the user's label for it, and Type one of the DownloadClient*
	// constants.
	Name string
	Type string
	// Error is why the last poll failed.
	Error string
	// Since is when the client was first seen as unreachable.
	Since time.Time
}
