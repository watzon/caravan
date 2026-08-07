package core

import "testing"

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
