package api

import (
	"net/http"

	"github.com/watzon/caravan/internal/core"
)

// adultVisible reports whether this request may see anything from the adult
// module: the server-wide switch is on AND the caller is granted (see
// core.AdultVisible for why both, and why an admin needs only the first).
//
// It is the single question every adult surface asks. Routes under /adult ask
// it through requireAdult; the shared surfaces that are not adult routes but
// can carry adult rows — discover, search, the calendar — ask it directly,
// because for them the answer is a filter rather than a door.
func (s *server) adultVisible(r *http.Request) (bool, error) {
	enabled, err := s.st.AdultEnabled(r.Context())
	if err != nil {
		return false, err
	}
	u := currentUser(r)
	return core.AdultVisible(enabled, u.Role, u.AdultAccess), nil
}

// requireAdult gates the whole /adult route subtree.
//
// It wraps the subtree rather than being called at the top of each handler,
// which is the point: a route added to the adult mux is gated because of where
// it was registered, not because somebody remembered a line. A handler that
// forgets its own check is a bug that ships silently; a route registered on the
// wrong mux is visible in the routing table.
//
// The refusal is 404, never 403. 403 would answer "this exists and you may not
// have it", which on a module whose whole promise is to be absent when off is a
// worse leak than the data — it tells an ungranted housemate, or anyone poking
// at a server with the module disabled, exactly which routes to come back for.
// 404 is also what an unrouted path returns, so the two are indistinguishable
// from outside.
//
// A member reaching an adult route that memberAllowed does not name is turned
// away by requireAuth first, with the same 403 every non-allowlisted route
// gives them — so that path leaks nothing either. Adult routes a member is
// meant to reach must be added to memberAllowed as well as to this subtree;
// this gate then decides whether they are granted.
func (s *server) requireAdult(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visible, err := s.adultVisible(r)
		if err != nil {
			s.writeStoreError(w, "read adult settings", err)
			return
		}
		if !visible {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}
