package searchql

import "testing"

// FuzzParse holds the two properties every caller depends on and no table of
// examples can prove: the language never panics on user input, and its
// canonical form is stable. A String that parsed back to a different
// expression would mean the search box silently rewrites what the user typed.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"",
		"   ",
		"dune part two",
		`"dune part two"`,
		`title:"Dune" year:2021`,
		`title:"Some Show" season:1 episode:2`,
		`site:"Creampie Thais" date:2026-01-19`,
		"dune OR arrakis",
		"(dune OR arrakis) -cam quality:1080p",
		"NOT (a b) OR -c",
		"Re:Zero",
		"title:",
		`dune "part`,
		"(((",
		"()",
		"dune OR",
		"-",
		`--x`,
		`"\\"`,
		`a"b"c:d`,
		"is:seasonpack bitdepth:10 date:26.01.19",
	} {
		f.Add(seed)
	}

	rel := movieRelease()
	f.Fuzz(func(t *testing.T, input string) {
		q, err := Parse(input)
		if err != nil {
			if q != nil {
				t.Fatalf("Parse(%q) returned both a query and %v", input, err)
			}
			if err.Error() == "" {
				t.Fatalf("Parse(%q) returned an empty error message", input)
			}
			return
		}
		q.Matches(rel)
		if got := q.UpstreamQueries(); (len(got) > 0) != q.HasUpstreamText() {
			t.Fatalf("Parse(%q): UpstreamQueries %v disagrees with HasUpstreamText", input, got)
		}
		canonical := q.String()
		again, err := Parse(canonical)
		if err != nil {
			t.Fatalf("Parse(%q).String() = %q, which does not parse: %v", input, canonical, err)
		}
		if second := again.String(); second != canonical {
			t.Fatalf("Parse(%q).String() = %q, which re-serializes as %q", input, canonical, second)
		}
		if !equalStrings(again.UpstreamQueries(), q.UpstreamQueries()) {
			t.Fatalf("Parse(%q): canonical form searches %v, original searches %v",
				input, again.UpstreamQueries(), q.UpstreamQueries())
		}
		if again.Matches(rel) != q.Matches(rel) {
			t.Fatalf("Parse(%q): canonical form matches differently", input)
		}
	})
}
