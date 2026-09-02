package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// adultProbe wires a handler that answers 204 behind requireAdult, so a test
// can tell "the gate refused" apart from "no such route", which, from outside,
// is exactly the distinction the gate is built to erase.
func adultProbe(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	s := &server{st: st, log: slog.Default()}
	return s.requireAdult(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})), st
}

// serveAs runs one request through h as the given identity, the way requireAuth
// would have resolved it.
func serveAs(h http.Handler, u requestUser) *httptest.ResponseRecorder {
	r := withRequestUser(httptest.NewRequest(http.MethodGet, "/adult/sites", nil), u)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// The gate's whole truth table, as HTTP. core.AdultVisible is tested on its own;
// this proves the wrapper asks it the right question with the right inputs and
// answers a refusal the right way.
func TestRequireAdultGatesTheSubtree(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		// granted names user 2 on the adult library. It is a store fact rather
		// than a field on the identity: a grant lives in `library_access`, and a
		// test that could set one on the request would be proving the gate reads
		// something no real request carries.
		granted bool
		user    requestUser
		want    int
	}{
		{
			name: "disabled hides it from an admin too",
			user: requestUser{Role: core.RoleAdmin},
			want: http.StatusNotFound,
		},
		{
			name:    "disabled hides it from a granted member",
			granted: true,
			user:    requestUser{ID: 2, Role: core.RoleMember},
			want:    http.StatusNotFound,
		},
		{
			name:    "enabled, an ungranted member still sees nothing",
			enabled: true,
			user:    requestUser{ID: 2, Role: core.RoleMember},
			want:    http.StatusNotFound,
		},
		{
			name:    "enabled, an admin needs no grant",
			enabled: true,
			user:    requestUser{ID: 1, Role: core.RoleAdmin},
			want:    http.StatusNoContent,
		},
		{
			name:    "enabled, a granted member is let through",
			enabled: true,
			granted: true,
			user:    requestUser{ID: 2, Role: core.RoleMember},
			want:    http.StatusNoContent,
		},
		{
			// The open server authenticates as an implicit admin, so it needs
			// only an active adult library. The same trusted-LAN default the
			// rest of the API has.
			name:    "enabled, the open server is an admin",
			enabled: true,
			user:    requestUser{Role: core.RoleAdmin, Open: true},
			want:    http.StatusNoContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, st := adultProbe(t)
			// A real row: a grant is a foreign key onto `users`, so an id
			// invented in the table would be a grant on nobody.
			member := createUser(t, st, testMember, testPassword, core.RoleMember)
			lib := enableAdultLibrary(t, st)
			if !tt.enabled {
				setAdultLibrariesActive(t, st, false)
			}
			if tt.granted {
				if err := st.SetLibraryAccess(t.Context(), lib.ID, true, []int64{member.ID}); err != nil {
					t.Fatalf("SetLibraryAccess: %v", err)
				}
			}
			user := tt.user
			if user.Role == core.RoleMember {
				user.ID = member.ID
			}
			rec := serveAs(h, user)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

// The refusal must be indistinguishable from a route that was never registered.
// 403 would answer "this exists and you may not have it", which on a module
// whose promise is to be absent is a worse leak than the data itself.
func TestRequireAdultRefusalLooksLikeAnUnroutedPath(t *testing.T) {
	h, _ := adultProbe(t)

	gated := serveAs(h, requestUser{Role: core.RoleAdmin})
	if gated.Code != http.StatusNotFound {
		t.Fatalf("gated status = %d, want %d", gated.Code, http.StatusNotFound)
	}

	// The same shape the router's own 404 produces, so a caller cannot tell the
	// two apart by the body either.
	srv, _, _ := newTestServer(t)
	unrouted := do(t, srv, http.MethodGet, "/api/v1/no-such-route", "")
	wantStatus(t, unrouted, http.StatusNotFound)
	if gated.Body.String() != unrouted.Body.String() {
		t.Errorf("gated body %q differs from an unrouted path's %q",
			gated.Body.String(), unrouted.Body.String())
	}
}

// The phase's "no adult routes respond when disabled" acceptance, asserted end
// to end against the real router with a real session.
//
// Its limits are worth stating, because they are the point: while the adult mux
// is empty, this cannot tell a gated route from an unregistered one: that is
// precisely the indistinguishability the gate exists to produce, and
// TestRequireAdultGatesTheSubtree above is what proves the gate itself works.
// This test becomes load-bearing the moment the first adult route is registered
// on that mux, and it is here now so that it does.
func TestAdultSubtreeIs404OnTheRealRouterWhenDisabled(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	cookie := login(t, h, testAdmin, testPassword)

	for _, path := range []string{"/api/v1/adult/", "/api/v1/adult/sites", "/api/v1/adult/sites/1"} {
		rec := doAuth(t, h, http.MethodGet, path, "", withCookie(cookie))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s as an admin with the module off = %d, want 404 (body %q)",
				path, rec.Code, rec.Body.String())
		}
	}
}

// Enabling the module is a decision about a library row (since 0011 the row is
// seeded dormant and the switch is its `active` flag) so adult_enabled must
// stay out of the PUT /settings allowlist, the way storage_root does. If it
// ever leaks in, a key-value write would claim the module is on while every
// gate still reads the dormant row and answers 404.
func TestPutSettingsRefusesTheAdultSwitch(t *testing.T) {
	h, st, _ := newTestServer(t)

	rec := do(t, h, http.MethodPut, "/api/v1/settings", `{"adult_enabled":"true"}`)
	wantStatus(t, rec, http.StatusBadRequest)

	settings, err := st.AllSettings(t.Context())
	if err != nil {
		t.Fatalf("AllSettings: %v", err)
	}
	if _, ok := settings[store.SettingAdultEnabled]; ok {
		t.Error("a rejected PUT still wrote adult_enabled")
	}
	lib, err := st.GetLibraryByKind(t.Context(), core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetLibraryByKind(adult): %v", err)
	}
	if lib.Active {
		t.Error("a rejected PUT switched the seeded adult library on")
	}
}

// The gate reads a grant from `library_access` on every request, and it must: a
// copy taken at login would leave a housemate whose access was revoked this
// morning still holding it until they logged out, and revoking a grant is
// exactly the moment somebody needs it to be instant.
//
// This is why the identity requireAuth resolves carries no grant of its own.
// Anything cached onto the session would be a permission outliving its
// revocation, and there would be no request at which to notice.
func TestTheGateReadsTheGrantPerRequest(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	s := &server{
		st: st, log: slog.Default(),
		sessions: newSessionStore(), sessionTTL: defaultSessionTTL,
	}
	member := createUser(t, st, testMember, testPassword, core.RoleMember)
	lib := enableAdultLibrary(t, st)
	if err := st.SetLibraryAccess(ctx, lib.ID, true, []int64{member.ID}); err != nil {
		t.Fatalf("SetLibraryAccess: %v", err)
	}
	token, err := s.sessions.issue(member.ID, s.sessionTTL)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}

	// /requests is member-allowed, so the member is not turned away before the
	// gate this test is about has been consulted.
	var (
		seen requestUser
		sees bool
		gErr error
	)
	h := s.requireAuth(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = currentUser(r)
		sees, gErr = s.gate(r).seesAdult(r.Context())
	}))
	call := func() bool {
		r := httptest.NewRequest(http.MethodGet, "/requests", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		h.ServeHTTP(httptest.NewRecorder(), r)
		if gErr != nil {
			t.Fatalf("seesAdult: %v", gErr)
		}
		return sees
	}

	if !call() || seen.ID != member.ID {
		t.Fatalf("granted member resolved as %+v seeing %v, want id %d with the shelf",
			seen, sees, member.ID)
	}

	if err := st.SetLibraryAccess(ctx, lib.ID, true, nil); err != nil {
		t.Fatalf("SetLibraryAccess(revoke): %v", err)
	}
	if call() {
		t.Error("after revoking, the same session still saw the library")
	}
}
