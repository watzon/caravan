package core

import "testing"

// The whole access rule, exhaustively. Every cell here is a decision somebody
// could get wrong in a way that either hides the module from its owner or shows
// it to a housemate nobody granted, so the table is written out in full rather
// than sampled.
func TestAdultVisible(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		role    string
		granted bool
		want    bool
	}{
		// The global switch is absolute. An admin does not bypass it: with the
		// module off it is absent, which is what makes "no routes, no UI, no
		// traffic" true for everyone including the person who owns the box.
		{name: "off, admin, granted", enabled: false, role: RoleAdmin, granted: true},
		{name: "off, admin, ungranted", enabled: false, role: RoleAdmin},
		{name: "off, member, granted", enabled: false, role: RoleMember, granted: true},
		{name: "off, member, ungranted", enabled: false, role: RoleMember},

		// On, an admin is implicitly granted: they hand out the grants, so
		// making them grant themselves buys nothing.
		{name: "on, admin, granted", enabled: true, role: RoleAdmin, granted: true, want: true},
		{name: "on, admin, ungranted", enabled: true, role: RoleAdmin, want: true},

		// On, a member needs the grant and nothing else stands in for it.
		{name: "on, member, granted", enabled: true, role: RoleMember, granted: true, want: true},
		{name: "on, member, ungranted", enabled: true, role: RoleMember},

		// An unrecognised role is not a role with permissions. It reaches here
		// only through a bug, and the safe reading of a bug is "not an admin".
		{name: "on, unknown role, granted", enabled: true, role: "wat", granted: true, want: true},
		{name: "on, unknown role, ungranted", enabled: true, role: "wat"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AdultVisible(tt.enabled, tt.role, tt.granted); got != tt.want {
				t.Errorf("AdultVisible(%t, %q, %t) = %t, want %t",
					tt.enabled, tt.role, tt.granted, got, tt.want)
			}
		})
	}
}

func TestValidSeriesKind(t *testing.T) {
	for _, tt := range []struct {
		kind string
		want bool
	}{
		{kind: SeriesKindTV, want: true},
		{kind: SeriesKindAdult, want: true},
		// The zero value is not a kind. A Series built without one must be
		// caught, not defaulted somewhere arbitrary.
		{kind: ""},
		{kind: "TV"},
		{kind: "documentary"},
	} {
		if got := ValidSeriesKind(tt.kind); got != tt.want {
			t.Errorf("ValidSeriesKind(%q) = %t, want %t", tt.kind, got, tt.want)
		}
	}
}
