package clients

import (
	"errors"
	"net/url"
	"strings"
)

// Clamp01 bounds a completion fraction to [0,1].
//
// Download clients report progress in their own units — a fraction, a percent,
// a byte pair — and a client that is still sizing a job can briefly report more
// than it has. core.DownloadStatus.Progress promises a fraction, so every
// backend narrows to one here rather than each inventing its own guard.
func Clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

// snippetMax is how much of a response body an error message may carry.
const snippetMax = 200

// Snippet trims a response body down to something an error message can carry.
//
// A download client's own complaint is one short line ("Fails.", "API Key
// Incorrect"); anything longer is an HTML error page from a reverse proxy or
// from whatever else is listening on that port, and quoting all of it into the
// activity feed helps nobody.
func Snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > snippetMax {
		s = s[:snippetMax] + "…"
	}
	return s
}

// Scrub strips the request URL out of a transport error.
//
// net/http answers a failed request with a *url.Error that quotes the whole
// URL back, and a download client's URL is not safe to repeat: SABnzbd takes
// its API key as a query parameter, an NZB link carries the indexer's, and a
// user may well have pasted credentials into the base URL as userinfo. The
// wrapped cause ("connection refused", "no such host") is the part worth
// showing and the only part that is safe to (SPEC §12).
func Scrub(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}
	return err
}
