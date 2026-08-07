package api

import (
	"context"
	"net/http"

	"github.com/watzon/caravan/internal/core"
)

// libraryGate is the per-library access rule as one request sees it.
//
// Every read surface in this package eventually asks the same question — "may
// the caller see the library this row belongs to" — and the answer is the same
// for the whole request. So it is resolved here, at most twice per request: one
// ListLibraries, and one library_access lookup for the calling account. An
// admin does not need the second, because core.LibraryVisible never consults a
// grant for one.
//
// It is a request-scoped VALUE rather than a cache on the server for the reason
// the ownership filters are: a grant revoked mid-request must not be answered
// from a map somebody built earlier, and a memo that outlives the request is a
// permission that outlives its revocation.
type libraryGate struct {
	srv  *server
	user requestUser

	loaded bool
	// libraries is every row, in ListLibraries' order; byID indexes it.
	libraries []core.Library
	byID      map[int64]core.Library
	// defaults is the library each kind's by-kind lookups resolve to, in
	// GetLibraryByKind's order (is_default first, then id). It is what makes a
	// row whose library_id is still 0 answerable.
	defaults map[string]int64
	// grants is the set of libraries this account holds an access row on.
	// Empty for an admin, who bypasses restriction entirely.
	grants map[int64]bool
}

// gateFor builds a gate that answers as u.
func (s *server) gateFor(u requestUser) *libraryGate {
	return &libraryGate{srv: s, user: u}
}

// gateContextKey is the private key the request's gate travels under, so no
// other package can substitute one.
type gateContextKey struct{}

// withLibraryGate returns r carrying g, so every helper the handler calls
// shares one gate — and so the two queries behind it happen once.
func withLibraryGate(r *http.Request, g *libraryGate) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), gateContextKey{}, g))
}

// gate is the request's library gate.
//
// requireAuth attaches one to every gated request. A request that never went
// through it — an auth-exempt path, or a unit test calling a handler directly —
// gets a fresh gate for currentUser's identity, which is the same identity
// those paths behave as anyway. GET /images is the one surface that must NOT
// use this: it is exempt, so currentUser would report an implicit admin (see
// artworkVisible).
func (s *server) gate(r *http.Request) *libraryGate {
	if g, ok := r.Context().Value(gateContextKey{}).(*libraryGate); ok {
		return g
	}
	return s.gateFor(currentUser(r))
}

// anonymousGate answers as a caller with an account but no grants: everything
// active and unrestricted, nothing else.
//
// It exists for the surfaces that have no identity to resolve and must not
// borrow one — the iCal feed, whose bearer URL names nobody (see
// handleCalendarICS).
func (s *server) anonymousGate() *libraryGate {
	return s.gateFor(requestUser{Role: core.RoleMember})
}

func (g *libraryGate) load(ctx context.Context) error {
	if g.loaded {
		return nil
	}
	libs, err := g.srv.st.ListLibraries(ctx)
	if err != nil {
		return err
	}
	g.libraries = libs
	g.byID = make(map[int64]core.Library, len(libs))
	g.defaults = make(map[string]int64, 3)
	for _, l := range libs {
		g.byID[l.ID] = l
		// GetLibraryByKind's ordering, reproduced: is_default first, then id.
		// Resolving a by-kind lookup differently here would hide rows the rest
		// of the server files under that library.
		if cur, ok := g.defaults[l.Kind]; !ok || (l.IsDefault && !g.byID[cur].IsDefault) {
			g.defaults[l.Kind] = l.ID
		}
	}

	g.grants = map[int64]bool{}
	if g.user.Role != core.RoleAdmin {
		// User id 0 — the API key and the open install — holds nothing, and
		// both authenticate as an admin anyway; asking is a query that can only
		// come back empty.
		if g.user.ID != 0 {
			g.grants, err = g.srv.st.ListLibraryAccessForUser(ctx, g.user.ID)
			if err != nil {
				return err
			}
		}
		// The adult module's own grant, bridged onto its libraries while the
		// switch still exists. store.SetUserAdultAccess dual-writes an access
		// row, so for a real account the two already agree — but an identity
		// resolved from a session carries the flag, and honouring it is what
		// keeps this generalization from narrowing anything. It goes away with
		// users.adult_access.
		if g.user.AdultAccess {
			for _, l := range libs {
				if l.Kind == core.LibraryKindAdult {
					g.grants[l.ID] = true
				}
			}
		}
	}
	g.loaded = true
	return nil
}

// library returns one library row and whether it exists at all.
func (g *libraryGate) library(ctx context.Context, id int64) (core.Library, bool, error) {
	if err := g.load(ctx); err != nil {
		return core.Library{}, false, err
	}
	lib, ok := g.byID[id]
	return lib, ok, nil
}

// visible answers for a row that names its library by id.
//
// Zero is "this row names no library", which is not a hiding place: an untied
// grab, a parked file from a plain scan and every pre-0022 item carry it, and
// they were visible before libraries had ids. A library id that resolves to no
// row is an orphan for the same reason the ownership filters preserve one —
// ownership that cannot be established is not evidence of ownership.
//
// A row whose library is known only by KIND asks visibleKind instead.
func (g *libraryGate) visible(ctx context.Context, libraryID int64) (bool, error) {
	if libraryID == 0 {
		return true, nil
	}
	lib, ok, err := g.library(ctx, libraryID)
	if err != nil {
		return false, err
	}
	if !ok {
		return true, nil
	}
	return g.allows(ctx, lib)
}

// allows applies the rule to a library ROW the caller already holds. It is the
// one place core.LibraryVisible is called with this request's identity, so
// there is exactly one place to read to know what a gate answers.
func (g *libraryGate) allows(ctx context.Context, lib core.Library) (bool, error) {
	if err := g.load(ctx); err != nil {
		return false, err
	}
	return core.LibraryVisible(lib, g.user.Role, g.grants[lib.ID]), nil
}

// visibleKind is visible for a row that may still be answering by kind: zero
// resolves to the kind's default library rather than waving the row through.
//
// A kind with no library at all is invisible, not open. That is the whole
// promise of absence in its general form: an adult series row on an install
// that never enabled the module belongs to a shelf that does not exist, and a
// shelf that does not exist shows nobody anything.
func (g *libraryGate) visibleKind(ctx context.Context, libraryID int64, kind string) (bool, error) {
	if err := g.load(ctx); err != nil {
		return false, err
	}
	if libraryID == 0 {
		id, ok := g.defaults[kind]
		if !ok {
			return false, nil
		}
		libraryID = id
	}
	return g.visible(ctx, libraryID)
}

// seesAdult reports whether this caller can see any active adult-kind library.
//
// It is the question the adult module's server-wide switch used to answer, and
// it is asked by every surface that speaks adult VOCABULARY — scene requests,
// the 6000 category block, the stash handoff's health — rather than by the
// surfaces that merely hold adult rows, which ask about the row's own library.
func (g *libraryGate) seesAdult(ctx context.Context) (bool, error) {
	if err := g.load(ctx); err != nil {
		return false, err
	}
	for _, l := range g.libraries {
		if l.Kind != core.LibraryKindAdult {
			continue
		}
		if core.LibraryVisible(l, g.user.Role, g.grants[l.ID]) {
			return true, nil
		}
	}
	return false, nil
}

// seesAll reports whether NOTHING is hidden from this caller.
//
// Two conditions, and the second is the one that is easy to forget: every
// library row must be visible, AND every kind a row can name must have a
// library to name. A row is hidden by the ABSENCE of its shelf just as surely
// as by a locked one — an adult series on an install with no adult library
// belongs nowhere, and nowhere is visible to nobody (see visibleKind).
//
// It buys the ownership filters their fast path: when nothing is hidden, a
// queue or history page needs no owner lookup per row at all, which is exactly
// what those pages cost before there was anything to hide.
func (g *libraryGate) seesAll(ctx context.Context) (bool, error) {
	if err := g.load(ctx); err != nil {
		return false, err
	}
	for _, l := range g.libraries {
		if !core.LibraryVisible(l, g.user.Role, g.grants[l.ID]) {
			return false, nil
		}
	}
	for _, kind := range []string{core.LibraryKindMovie, core.LibraryKindTV, core.LibraryKindAdult} {
		if _, ok := g.defaults[kind]; !ok {
			return false, nil
		}
	}
	return true, nil
}

// requireAdult gates the whole /adult route subtree.
//
// It wraps the subtree rather than being called at the top of each handler,
// which is the point: a route added to the adult mux is gated because of where
// it was registered, not because somebody remembered a line. A handler that
// forgets its own check is a bug that ships silently; a route registered on the
// wrong mux is visible in the routing table.
//
// The question it asks is seesAdult: the routes speak the adult vocabulary, so
// what they need is an adult shelf the caller can see, not a particular one.
//
// The refusal is 404, never 403. 403 would answer "this exists and you may not
// have it", which on a surface whose whole promise is to be absent when off is
// a worse leak than the data — it tells an ungranted housemate, or anyone
// poking at a server with no adult library, exactly which routes to come back
// for. 404 is also what an unrouted path returns, so the two are
// indistinguishable from outside.
//
// A member reaching an adult route that memberAllowed does not name is turned
// away by requireAuth first, with the same 403 every non-allowlisted route
// gives them — so that path leaks nothing either. Adult routes a member is
// meant to reach must be added to memberAllowed as well as to this subtree;
// this gate then decides whether they are granted.
func (s *server) requireAdult(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visible, err := s.gate(r).seesAdult(r.Context())
		if err != nil {
			s.writeStoreError(w, "read library access", err)
			return
		}
		if !visible {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}
