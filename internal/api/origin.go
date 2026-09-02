package api

import (
	"net/http"
	"net/url"
	"strings"
)

// Cross-site request forgery, and why the password is not what defends against
// it (SPEC §11).
//
// Caravan's documented default is no password on a LAN, and the Docker image
// binds every interface. In that state every /api/v1 route is reachable by
// anyone who can make the owner's browser send a request, which any web page
// can do, with no LAN foothold of its own. A form POST with
// enctype="text/plain" is a CORS "simple request": no preflight, no need to
// read the reply. That is enough to POST /system/shutdown, to queue a storage
// migration that moves the library off the drive, or (because setting the first
// password needs no proof of the old one) to lock the owner out of their own
// box.
//
// The session cookie's SameSite=Lax closes this once a password exists. The
// guard below closes it in the state that ships.
//
// It is written as two independent checks because browsers differ in which
// header they send, and neither is a credential: a request from a non-browser
// (curl, a calendar app, an external tool with the API key) carries neither and
// is allowed through. CSRF is a browser-only attack, so a browser-only defence
// is the whole of it.
func requireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Safe methods are not gated. A cross-site GET can be made either way
		// and the attacker cannot read the reply without CORS, which this
		// server never grants.
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross-site request blocked")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin reports whether an unsafe request may proceed.
func sameOrigin(r *http.Request) bool {
	// Fetch metadata, which current browsers attach to every request and no
	// page can forge. "same-origin" is the SPA; "none" is a user typing a URL.
	// "same-site" is another host under the same registrable domain, which for
	// a LAN box is still somebody else.
	switch r.Header.Get("Sec-Fetch-Site") {
	case "cross-site", "same-site":
		return false
	case "same-origin", "none":
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		// No Origin and no fetch metadata: not a browser request.
		return true
	}
	// "null", a sandboxed iframe or a document from a data: URL, parses to an
	// empty host and is refused, which is the intent.
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

// formContentTypes are the three encodings an HTML form can post without a
// preflight. decodeJSON refuses them outright: json.Decoder ignores whatever
// follows the first JSON value, so a form field *named*
// `{"root":"/data/attacker"}` with an empty value decodes cleanly as that
// object. Nothing legitimate posts JSON under these types, so rejecting them
// costs no caller anything.
var formContentTypes = map[string]bool{
	"text/plain":                        true,
	"application/x-www-form-urlencoded": true,
	"multipart/form-data":               true,
}

// formEncoded reports whether the request declares an HTML-form content type.
func formEncoded(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return false
	}
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return formContentTypes[strings.ToLower(strings.TrimSpace(ct))]
}
