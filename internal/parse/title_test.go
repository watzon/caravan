package parse

import "testing"

func TestTitleSlugNormalizesSpellingVariants(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "The Matrix", want: "matrix"},
		{in: "Marvel's Agents of S.H.I.E.L.D.", want: "marvels agents of shield"},
		{in: "Fast & Furious", want: "fast and furious"},
		{in: "Amélie", want: "amelie"},
		{in: "Blade Runner 2049: The Final Cut", want: "blade runner 2049 the final cut"},
	}
	for _, test := range tests {
		if got := TitleSlug(test.in); got != test.want {
			t.Fatalf("TitleSlug(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

func TestSameTitleFlagsOnlyRealMismatches(t *testing.T) {
	same := [][2]string{
		{"The Matrix", "Matrix"},
		{"Marvel's Agents of S.H.I.E.L.D.", "Marvels Agents of SHIELD"},
		{"Amélie", "Amelie"},
		{"Dune", ""}, // unparsed title is "cannot tell", not a mismatch
	}
	for _, pair := range same {
		if !SameTitle(pair[0], pair[1]) {
			t.Fatalf("SameTitle(%q, %q) = false, want true", pair[0], pair[1])
		}
	}
	different := [][2]string{
		{"Dune", "S3XUS E23 Laney Grey And Octavia Red Inception XXX"},
		{"Dune", "Dune Part Two"},
		{"Inception", "Inception Explained Fan Documentary"},
	}
	for _, pair := range different {
		if SameTitle(pair[0], pair[1]) {
			t.Fatalf("SameTitle(%q, %q) = true, want false", pair[0], pair[1])
		}
	}
}
