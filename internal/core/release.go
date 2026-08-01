package core

import "time"

// Indexer types. Torznab carries torrent results, Newznab carries Usenet
// results; both speak the same XML dialect, which is why one client covers
// them (SPEC §5.1).
const (
	IndexerTypeTorznab = "torznab"
	IndexerTypeNewznab = "newznab"
)

// IndexerConfig is a configured search source (SPEC §5.1, `indexers`).
//
// Caravan ships with none preconfigured (SPEC §12): every entry here is
// something the user added.
type IndexerConfig struct {
	ID int64
	// Name is the user-facing label, unique across indexers.
	Name string
	// URL is the indexer's base API URL, without the `/api` path or query.
	URL string
	// APIKey is the credential. It lives in the database, never in the
	// bootstrap YAML and never in logs (SPEC §12).
	APIKey string
	// Type is one of the IndexerType* constants.
	Type string
	// Categories are the indexer category ids to search by default. Empty
	// means "let the caller decide".
	Categories []int
	// Enabled excludes the indexer from search fan-out when false, without
	// losing its configuration.
	Enabled bool
}

// Release protocols. The protocol decides which engine a grab is routed to
// (SPEC §5.1).
const (
	ProtocolTorrent = "torrent"
	ProtocolUsenet  = "usenet"
)

// Release is one search result from an indexer, already run through the
// release parser.
//
// A Release is a claim, not a fact: everything except ID and IndexerID comes
// from the indexer, and Parsed is the parser's reading of Title. It is cached
// in the `releases` table so later searches can recognize something already
// seen, but losing that cache costs nothing but a re-search.
type Release struct {
	// ID is the `releases` row id, 0 for a result that has not been cached.
	ID int64
	// IndexerID references indexers.id. It is a soft reference: the indexer
	// may have been deleted since the result was cached.
	IndexerID int64
	// Indexer is the indexer's display name, denormalized so a cached result
	// still says where it came from after the indexer is deleted.
	Indexer string
	// Title is the release name as the indexer published it.
	Title string
	// GUID is the indexer's identifier for this result. Unique per indexer,
	// and what deduplicates repeat sightings.
	GUID string
	// DownloadURL is the .torrent/.nzb URL, or a magnet link.
	DownloadURL string
	// InfoHash is the torrent info hash when the indexer supplied one; empty
	// for Usenet and for indexers that do not publish it.
	InfoHash string
	// Protocol is one of the Protocol* constants.
	Protocol string
	// Size is the release size in bytes, 0 when the indexer did not say.
	Size int64
	// Seeders and Leechers are torrent swarm counts, 0 for Usenet.
	Seeders  int
	Leechers int
	// PublishedAt is when the indexer published the release, zero when unknown.
	PublishedAt time.Time
	// Parsed is what the release parser made of Title.
	Parsed ParsedRelease
}
