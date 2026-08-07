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
