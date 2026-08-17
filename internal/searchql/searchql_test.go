package searchql

import (
	"strings"
	"testing"
)

// TestParseCanonicalizesInput checks both halves of the grammar at once: that
// each input reads as the intended expression, and that String writes it back
// in a form that parses to the same thing.
func TestParseCanonicalizesInput(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain words", "dune part two", "dune part two"},
		{"quoted phrase stays one term", `"dune part two"`, `"dune part two"`},
		{"single quoted word loses its quotes", `"dune"`, "dune"},
		{"explicit AND is adjacency", "dune AND arrakis", "dune arrakis"},
		{"OR", "dune OR arrakis", "dune OR arrakis"},
		{"OR binds looser than adjacency", "dune sand OR arrakis", "dune sand OR arrakis"},
		{"parentheses group the OR", "(dune OR arrakis) 1080p", "(dune OR arrakis) 1080p"},
		{"redundant parentheses drop", "(dune)", "dune"},
		{"NOT", "dune NOT cam", "dune NOT cam"},
		{"hyphen is NOT", "dune -cam", "dune NOT cam"},
		{"hyphen before a phrase", `dune -"web rip"`, `dune NOT "web rip"`},
		{"hyphen before a field term", "dune -quality:480p", "dune NOT quality:480p"},
		{"NOT of a group", "NOT (dune OR arrakis)", "NOT (dune OR arrakis)"},
		{"field terms", `title:"Some Show" season:1 episode:2`, `title:"Some Show" season:1 episode:2`},
		{"field name is case insensitive", "TITLE:dune", "title:dune"},
		{"quoted field value keeps its quotes when it needs them", `site:"Creampie Thais"`, `site:"Creampie Thais"`},
		{"unknown field is a keyword", "Re:Zero", "Re:Zero"},
		{"empty field value is a keyword", "title:", `"title:"`},
		{"colon inside a phrase is text", `"year:2021"`, `"year:2021"`},
		{"lowercase or is a keyword", "sex lies and videotape", "sex lies and videotape"},
		{"lowercase not is a keyword", "not the nine oclock news", "not the nine oclock news"},
		{"quoted OR is a keyword", `dune "OR" arrakis`, `dune "OR" arrakis`},
		{"is terms", "is:proper is:seasonpack", "is:proper is:seasonpack"},
		{"escaped quote inside a phrase", `title:"The \"Great\" Movie"`, `title:"The \"Great\" Movie"`},
		{"parenthesis needs no surrounding space", "(a OR b)c", "(a OR b) c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := Parse(tc.input)
			if err != nil {
				t.Fatalf("Parse(%q) = %v", tc.input, err)
			}
			if got := q.String(); got != tc.want {
				t.Fatalf("Parse(%q).String() = %q, want %q", tc.input, got, tc.want)
			}
			again, err := Parse(q.String())
			if err != nil {
				t.Fatalf("reparsing %q = %v", q.String(), err)
			}
			if got := again.String(); got != tc.want {
				t.Fatalf("reparsing %q = %q, want %q", q.String(), got, tc.want)
			}
		})
	}
}

// TestParseRejectsOnlyBrokenStructure pins the error messages, which are shown
// in the search box rather than logged.
func TestParseRejectsOnlyBrokenStructure(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"unclosed quote", `dune "part two`, "unclosed quote"},
		{"unclosed quote in a field value", `title:"dune`, "unclosed quote"},
		{"unclosed parenthesis", "(dune OR arrakis", "unclosed parenthesis"},
		{"surplus closing parenthesis", "dune)", "unexpected closing parenthesis"},
		{"closing parenthesis after a group", "(dune))", "unexpected closing parenthesis"},
		{"empty parentheses", "dune ()", "empty parentheses"},
		{"trailing OR", "dune OR", `query ends with "OR"`},
		{"trailing AND", "dune AND", `query ends with "AND"`},
		{"trailing NOT", "dune NOT", `query ends with "NOT"`},
		{"leading OR", "OR dune", `unexpected "OR"`},
		{"empty input", "", "empty query"},
		{"whitespace only", "   \t ", "empty query"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := Parse(tc.input)
			if err == nil {
				t.Fatalf("Parse(%q) = %q, want an error", tc.input, q.String())
			}
			if err.Error() != tc.want {
				t.Fatalf("Parse(%q) error = %q, want %q", tc.input, err, tc.want)
			}
		})
	}
}

func TestParseAcceptsAwkwardButMeaningfulInput(t *testing.T) {
	// None of these is a mistake the language may refuse: every one names
	// something a release could genuinely be called.
	for _, input := range []string{
		"Re:Zero", "AC/DC", "9 1/2 Weeks", "WALL-E", "-", "F.T.L:Faster Than Light",
		"and", "or", "not", "Him & Her", "Æon Flux", `"("`,
	} {
		if _, err := Parse(input); err != nil {
			t.Fatalf("Parse(%q) = %v, want no error", input, err)
		}
	}
}

func TestParseTreatsHyphenInsideAWordAsText(t *testing.T) {
	// Only a leading hyphen negates. "Spider-Man" is one keyword, and the
	// upstream query has to carry the hyphen through untouched.
	q, err := Parse("Spider-Man 2021")
	if err != nil {
		t.Fatalf("Parse = %v", err)
	}
	if got := q.String(); got != "Spider-Man 2021" {
		t.Fatalf("String = %q", got)
	}
	if got := q.UpstreamQueries(); !equalStrings(got, []string{"Spider-Man 2021"}) {
		t.Fatalf("UpstreamQueries = %v", got)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func mustParse(t *testing.T, input string) *Query {
	t.Helper()
	q, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse(%q) = %v", input, err)
	}
	return q
}

func TestQuotingSurvivesValuesThatLookLikeSyntax(t *testing.T) {
	// quoteText and quoteValue exist to make String reversible. These are the
	// values that would otherwise come back as something else.
	for _, input := range []string{
		`"OR"`, `"NOT"`, `"-cam"`, `""`, `"a(b"`, `"year:2021"`, `title:"a b"`,
		`title:"a(b)"`, `title:"say \"hi\""`,
	} {
		first := mustParse(t, input).String()
		second := mustParse(t, first).String()
		if first != second {
			t.Fatalf("Parse(%q).String() = %q, which re-serializes as %q", input, first, second)
		}
		if strings.TrimSpace(first) == "" {
			t.Fatalf("Parse(%q).String() lost the term entirely", input)
		}
	}
}
