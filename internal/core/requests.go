package core

import "time"

// Request statuses. They are stored verbatim in requests.status and constrained
// by a CHECK in migration 0009.
const (
	// RequestPending is a wish nobody has acted on yet. At most one pending
	// request exists per (MediaType, TMDBID) — a second one merges into it.
	RequestPending = "pending"
	// RequestApproved means the title reached the library, either because a
	// request was approved or because it was added by some other path and the
	// pending request was absorbed.
	RequestApproved = "approved"
	// RequestDismissed means the request was turned down. It stays on the
	// table as history and stops blocking a fresh request for the same title.
	RequestDismissed = "dismissed"
)

// Request is a wish for something that is not in the library yet. It carries
// its own copy of the title, year and poster path rather than a foreign key:
// the whole point is that no library row exists.
type Request struct {
	ID int64
	// MediaType is MediaTypeMovie or MediaTypeSeries.
	MediaType string
	TMDBID    int64
	Title     string
	Year      int
	// PosterPath is the provider's poster path ("/abc.jpg"), empty when the
	// title has no artwork. It is a provider path, not a storage-root path:
	// nothing has been downloaded yet.
	PosterPath string
	// Seasons are the requested season numbers, ascending and deduplicated.
	// Nil means the whole title — every movie request, and a series request
	// that names no seasons.
	Seasons []int
	// MinAvailability is the release stage the asker wants the movie held for
	// (an Availability* constant), empty when unspecified — every series
	// request, and a movie request that left the default alone. Approving the
	// request carries it onto the movie row.
	MinAvailability string
	Status          string
	// RequestedBy is the account that asked, or 0 when no account did: a row
	// that predates accounts, or one made while the server runs open. It is
	// deliberately an id and not a name — usernames change, and a request is a
	// record of what happened. It is also not a foreign key: deleting a
	// housemate must not delete the history of what they asked for.
	RequestedBy int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
