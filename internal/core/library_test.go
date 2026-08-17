package core

import "testing"

// The whole per-library access rule, exhaustively. Every cell is a decision
// somebody could get wrong in a way that either hides a library from its owner
// or shows one to a housemate nobody granted, so the table is written out in
// full rather than sampled — the same treatment TestAdultVisible gives the rule
// this one generalizes.
func TestLibraryVisible(t *testing.T) {
	tests := []struct {
		name       string
		active     bool
		restricted bool
		role       string
		granted    bool
		want       bool
	}{
		// Inactive binds EVERYONE. An admin does not bypass it: deactivating a
		// library is how an owner hides one from themselves, and a switch its
		// holder cannot feel is not a switch. It is also what makes "no scans,
		// no provider traffic, no DLNA container" true rather than aspirational.
		{name: "inactive, admin, granted", role: RoleAdmin, granted: true},
		{name: "inactive, admin, restricted", restricted: true, role: RoleAdmin, granted: true},
		{name: "inactive, member, granted", granted: true, role: RoleMember},
		{name: "inactive, member, ungranted", role: RoleMember},

		// Active and open is every account's library, grant or no grant — the
		// state every library on an upgraded install is already in.
		{name: "open, admin", active: true, role: RoleAdmin, want: true},
		{name: "open, member, ungranted", active: true, role: RoleMember, want: true},
		{name: "open, member, granted", active: true, granted: true, role: RoleMember, want: true},

		// Active and restricted: a member needs the grant and nothing stands in
		// for it.
		{name: "restricted, member, granted", active: true, restricted: true,
			role: RoleMember, granted: true, want: true},
		{name: "restricted, member, ungranted", active: true, restricted: true, role: RoleMember},

		// An admin bypasses restriction even with an empty roster, and that is
		// the case that matters: the API-key credential and the open install
		// both authenticate as an admin with user id 0, which can never hold a
		// library_access row. If this cell ever reads false, both are locked out
		// of every restricted library with no door left to grant through.
		{name: "restricted, admin, ungranted", active: true, restricted: true,
			role: RoleAdmin, want: true},
		{name: "restricted, admin, granted", active: true, restricted: true,
			role: RoleAdmin, granted: true, want: true},

		// An unrecognised role is not a role with permissions. It reaches here
		// only through a bug, and the safe reading of a bug is "not an admin".
		{name: "restricted, unknown role, granted", active: true, restricted: true,
			role: "wat", granted: true, want: true},
		{name: "restricted, unknown role, ungranted", active: true, restricted: true, role: "wat"},
		{name: "inactive, unknown role, granted", restricted: true, role: "wat", granted: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lib := Library{Active: tt.active, Restricted: tt.restricted}
			if got := LibraryVisible(lib, tt.role, tt.granted); got != tt.want {
				t.Errorf("LibraryVisible({active:%t, restricted:%t}, %q, %t) = %t, want %t",
					tt.active, tt.restricted, tt.role, tt.granted, got, tt.want)
			}
		})
	}
}

// The acceptance rule, exhaustively. It is the one statement of which shelves
// an item may sit on, so the whole truth table lives beside it: every add, every
// move and every library resolver reads its answer, and a widening written into
// one of them instead would be a rule with more than one home.
func TestLibraryKindAccepts(t *testing.T) {
	kinds := []string{LibraryKindMovie, LibraryKindTV, LibraryKindAnime, LibraryKindAdult}
	want := map[string]map[string]bool{
		// A movie library holds films and nothing else.
		LibraryKindMovie: {LibraryKindMovie: true},
		// A television library holds television series, and takes back a row
		// filed as anime — which is what makes the anime shelf a place a series
		// can be moved OFF as well as onto.
		LibraryKindTV: {LibraryKindTV: true, LibraryKindAnime: true},
		// The one shelf that speaks two vocabularies.
		LibraryKindAnime: {LibraryKindMovie: true, LibraryKindTV: true, LibraryKindAnime: true},
		// No widening reaches the adult kind in either direction.
		LibraryKindAdult: {LibraryKindAdult: true},
	}
	for _, lib := range kinds {
		for _, item := range kinds {
			if got := LibraryKindAccepts(lib, item); got != want[lib][item] {
				t.Errorf("LibraryKindAccepts(%q, %q) = %t, want %t", lib, item, got, want[lib][item])
			}
		}
	}
	if LibraryKindAccepts("music", "music") != true {
		t.Error("LibraryKindAccepts is equality-first; an unknown kind must accept itself")
	}
	if LibraryKindAccepts(LibraryKindAnime, "music") {
		t.Error("LibraryKindAccepts(anime, music) = true, want unknown vocabularies refused")
	}
}

// The two directions of the series/library mapping must be inverses on the
// kinds a series can actually carry, or a move would write a `kind` the store
// then refuses to file under the destination (store.UpsertSeries).
func TestSeriesKindAndLibraryKindAreInverses(t *testing.T) {
	for _, kind := range []string{SeriesKindTV, SeriesKindAnime, SeriesKindAdult} {
		if got := SeriesKindForLibrary(LibraryKindForSeries(kind)); got != kind {
			t.Errorf("SeriesKindForLibrary(LibraryKindForSeries(%q)) = %q, want %q", kind, got, kind)
		}
	}
	// A movie library has no series vocabulary; it answers television, and the
	// store refuses the write loudly rather than this function guessing better.
	if got := SeriesKindForLibrary(LibraryKindMovie); got != SeriesKindTV {
		t.Errorf("SeriesKindForLibrary(movie) = %q, want %q", got, SeriesKindTV)
	}
}

func TestValidLibraryKind(t *testing.T) {
	for _, kind := range []string{LibraryKindMovie, LibraryKindTV, LibraryKindAnime, LibraryKindAdult} {
		if !ValidLibraryKind(kind) {
			t.Errorf("ValidLibraryKind(%q) = false, want true", kind)
		}
	}
	for _, kind := range []string{"", "music", "TV", "anime "} {
		if ValidLibraryKind(kind) {
			t.Errorf("ValidLibraryKind(%q) = true, want false", kind)
		}
	}
}
