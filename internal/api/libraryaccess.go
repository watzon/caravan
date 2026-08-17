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
	for _, l := range libs {
		g.byID[l.ID] = l
	}

	// `library_access` is the whole answer. users.adult_access was bridged onto
	// adult libraries during prerelease development; the baseline has no such
	// column, so it cannot be consulted again. It had stopped being writable when
	// the access API replaced the module switch, which made every value in it
	// stale by construction — an account whose grant was revoked through PUT
	// /libraries/{id}/access still carried a 1, and reading it would have handed
	// back the access that was just taken away.
	g.grants = map[int64]bool{}
	if g.user.Role != core.RoleAdmin && g.user.ID != 0 {
		// User id 0 — the API key and the open install — holds nothing, and
		// both authenticate as an admin anyway; asking is a query that can only
		// come back empty.
		g.grants, err = g.srv.st.ListLibraryAccessForUser(ctx, g.user.ID)
		if err != nil {
			return err
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

// visible answers for a row that names its library by id, which since
// migration 0011 is every movie and every series: 0011 stamped the rows that
// still carried a zero onto their kind's default, so there is no longer a
// by-KIND spelling of ownership for this to resolve.
//
// Zero survives for the rows where naming no library is the truth rather than a
// gap — an untied grab before its payload is filed, a file parked by a plain
// scan — and it is not a hiding place: those were visible before libraries had
// ids and nothing about them became secret. A library id that resolves to no
// row is an orphan for the same reason the ownership filters preserve one —
// ownership that cannot be established is not evidence of ownership.
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

// manages answers for an admin MANAGEMENT surface: the same rule as allows,
// with `active` lifted.
//
// Lifting it is what keeps the Active toggle reachable. A library switched off
// is dormant for every content route including an admin's, but the settings
// card behind it — the list it appears in, its PATCH, its access roster — has
// to keep answering, or switching a library off would be a one-way door with no
// handle on the far side.
//
// Restriction it does NOT lift, and the difference is deliberate: every caller
// of this is on an admin-only route, so core.LibraryVisible already waves an
// admin past restriction, and writing a second bypass here would be a rule with
// two homes.
func (g *libraryGate) manages(ctx context.Context, lib core.Library) (bool, error) {
	lib.Active = true
	return g.allows(ctx, lib)
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

// visibleLibraries is every library this caller may see, in ListLibraries'
// order. Inactive rows are absent, because core.LibraryVisible refuses them for
// everybody — see meResponse.Libraries for why that is right even for an admin.
func (g *libraryGate) visibleLibraries(ctx context.Context) ([]core.Library, error) {
	if err := g.load(ctx); err != nil {
		return nil, err
	}
	out := make([]core.Library, 0, len(g.libraries))
	for _, l := range g.libraries {
		if core.LibraryVisible(l, g.user.Role, g.grants[l.ID]) {
			out = append(out, l)
		}
	}
	return out, nil
}

// seesAll reports whether NOTHING is hidden from this caller: every library row
// is visible to it.
//
// One condition, since every item row names its shelf by id (see visible). It
// used to carry a second — that every kind must HAVE a library, because a row
// resolving by kind was hidden by the absence of its shelf — and migration 0011
// spent that: a row can only name a library that exists, so there is no kind
// whose absence hides anything.
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
	return true, nil
}

// libraryAccessUserJSON is one account and its standing on one library, for the
// Access card on the library's settings page.
//
// It is a DTO of its own rather than a field on userJSON so that GET /users —
// reachable on every install — carries no access field at all. A per-account
// flag on a general roster would say which libraries exist and who was kept out
// of them, which is the trace a restricted shelf exists not to leave.
type libraryAccessUserJSON struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	// Granted is whether the account holds a `library_access` row. It is
	// meaningless beside AlwaysGranted, and false on most admins.
	Granted bool `json:"granted"`
	// AlwaysGranted says the account reaches the library through its ROLE
	// rather than through a grant, which is true of every admin
	// (core.LibraryVisible). The card shows "Always has access" in place of a
	// checkbox, because a checkbox that changes nothing is a lie about who can
	// see the shelf.
	AlwaysGranted bool `json:"always_granted"`
}

// libraryAccessJSON is one library's whole access decision: the flag and the
// roster it applies to, together, because neither means anything alone.
type libraryAccessJSON struct {
	Restricted bool                    `json:"restricted"`
	Users      []libraryAccessUserJSON `json:"users"`
}

// libraryAccessRequest is the body of PUT /libraries/{id}/access.
//
// The whole decision, every time: the flag and the complete roster. There is no
// per-user toggle route, deliberately — restricting a library and naming who
// keeps it are one decision, and split across two requests there is a window in
// which the library is restricted to nobody and a member watching the screen
// sees a shelf vanish that was never meant to leave (store.SetLibraryAccess
// writes the pair in one transaction for the same reason).
type libraryAccessRequest struct {
	// Restricted is a pointer so an absent field is a client bug rather than a
	// silent unrestricting, the way monitorRequest treats Monitored.
	Restricted *bool `json:"restricted"`
	// UserIDs is the entire allow-list. Absent and empty are the same thing —
	// "nobody but the admins" — which is a legitimate state, and the reason
	// Restricted is a separate flag rather than an inference from this list.
	UserIDs []int64 `json:"user_ids"`
}

// handleGetLibraryAccess answers the Access card: the library's restriction and
// every account beside it.
//
// It resolves through manageableLibrary, so an INACTIVE library's access stays
// editable. An owner who switched a library off and then wanted to fix who may
// see it before switching it back on would otherwise have to make it visible to
// the wrong people first.
//
// Admin-only by absence from memberAllowed — a member who could read this would
// learn the household's account roster, which is the one thing a failed login
// goes out of its way not to confirm.
func (s *server) handleGetLibraryAccess(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	lib, ok := s.manageableLibrary(w, r, id)
	if !ok {
		return
	}
	s.writeLibraryAccess(w, r, *lib)
}

// handleSetLibraryAccess writes a library's restriction and its whole roster.
//
// Restricting also clears dlna_visible, in the store and in the same
// transaction (store.SetLibraryAccess): DLNA has no accounts, so "restricted to
// two people" and "advertised to every device on the LAN" cannot both be true.
// Re-sharing afterwards is a second, deliberate act on the Reach card.
//
// Named accounts are checked to exist before anything is written. A grant to an
// id that names nobody is not harmless — it is a row that will match whichever
// account is created with that id next, which is a permission arriving by
// accident.
func (s *server) handleSetLibraryAccess(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body libraryAccessRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Restricted == nil {
		writeError(w, http.StatusBadRequest, "restricted is required")
		return
	}
	ctx := r.Context()

	lib, ok := s.manageableLibrary(w, r, id)
	if !ok {
		return
	}
	users, err := s.st.ListUsers(ctx)
	if err != nil {
		s.writeStoreError(w, "list users", err)
		return
	}
	known := make(map[int64]bool, len(users))
	for _, u := range users {
		known[u.ID] = true
	}
	for _, uid := range body.UserIDs {
		if !known[uid] {
			writeError(w, http.StatusBadRequest, "no such user")
			return
		}
	}

	if err := s.st.SetLibraryAccess(ctx, lib.ID, *body.Restricted, body.UserIDs); err != nil {
		s.writeStoreError(w, "set library access", err)
		return
	}
	// Re-read: restricting clears dlna_visible, so the row the caller holds is
	// already stale in a way the next screen would render wrongly.
	fresh, err := s.st.GetLibrary(ctx, lib.ID)
	if err != nil {
		s.writeStoreError(w, "get library", err)
		return
	}
	s.writeLibraryAccess(w, r, *fresh)
}

// writeLibraryAccess renders the card from the library row and the account
// list, so a read and a write answer with the same body and the screen never
// has to guess what a write did to the rest of it.
func (s *server) writeLibraryAccess(w http.ResponseWriter, r *http.Request, lib core.Library) {
	ctx := r.Context()
	users, err := s.st.ListUsers(ctx)
	if err != nil {
		s.writeStoreError(w, "list users", err)
		return
	}
	granted, err := s.st.ListLibraryAccess(ctx, lib.ID)
	if err != nil {
		s.writeStoreError(w, "list library access", err)
		return
	}
	held := make(map[int64]bool, len(granted))
	for _, uid := range granted {
		held[uid] = true
	}
	rows := make([]libraryAccessUserJSON, 0, len(users))
	for _, u := range users {
		rows = append(rows, libraryAccessUserJSON{
			ID:            u.ID,
			Username:      u.Username,
			Role:          u.Role,
			Granted:       held[u.ID],
			AlwaysGranted: u.Role == core.RoleAdmin,
		})
	}
	writeJSON(w, http.StatusOK, libraryAccessJSON{Restricted: lib.Restricted, Users: rows})
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
