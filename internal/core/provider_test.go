package core

import (
	"strings"
	"testing"
)

// Every descriptor must be internally consistent and every kind must have a
// default that actually serves it — the create form and migration 0022 both
// lean on that agreement.
func TestProviderRegistryAgreement(t *testing.T) {
	kinds := []string{LibraryKindMovie, LibraryKindTV, LibraryKindAdult}
	for _, kind := range kinds {
		def := DefaultProviderForKind(kind)
		if def == "" {
			t.Errorf("DefaultProviderForKind(%q) = %q, want a provider id", kind, def)
			continue
		}
		if !ProviderServes(def, kind) {
			t.Errorf("default provider %q does not serve kind %q", def, kind)
		}
	}
	for _, p := range Providers() {
		if p.ID == "" || p.Name == "" || len(p.Kinds) == 0 {
			t.Errorf("descriptor %+v is missing an id, name or kinds", p)
		}
	}
}

func TestProviderServesRejectsMismatches(t *testing.T) {
	cases := []struct {
		id, kind string
		want     bool
	}{
		{ProviderTMDB, LibraryKindMovie, true},
		{ProviderTMDB, LibraryKindTV, true},
		{ProviderTMDB, LibraryKindAdult, false},
		{ProviderStashbox, LibraryKindAdult, true},
		{ProviderStashbox, LibraryKindMovie, false},
		// An instance is answered on its base: every configured stash-box
		// endpoint speaks stash-box, and none of them speaks TMDB's vocabulary.
		{ProviderStashbox + ":stashdb", LibraryKindAdult, true},
		{ProviderStashbox + ":stashdb", LibraryKindMovie, false},
		{ProviderStashbox + ":stashdb", LibraryKindTV, false},
		// A malformed instance id serves nothing rather than falling back to its
		// base — a chain that only consults ProviderServes must not admit an id
		// no registry lookup can ever resolve to a client.
		{ProviderStashbox + ":Bad", LibraryKindAdult, false},
		{ProviderTMDB + ":anything", LibraryKindMovie, false},
		{ProviderAniList, LibraryKindTV, true},
		// AniList is television-only: internal/anilist refuses movie lookups
		// with ErrProviderKindUnsupported, so a movie library must not be
		// creatable against it.
		{ProviderAniList, LibraryKindMovie, false},
		{ProviderAniList, LibraryKindAdult, false},
		{ProviderTVmaze, LibraryKindTV, true},
		// TVmaze catalogues television only: internal/tvmaze refuses movie
		// lookups with ErrProviderKindUnsupported, so a movie library must not
		// be creatable against it.
		{ProviderTVmaze, LibraryKindMovie, false},
		{ProviderTVmaze, LibraryKindAdult, false},
		{ProviderTheTVDB, LibraryKindTV, true},
		// TheTVDB DOES catalogue films, and the movie kind is refused anyway:
		// MovieMeta.DigitalRelease gates minimum availability and TheTVDB's movie
		// record has no typed release list to fill it from, so a movie library
		// chained here would start grabbing films that are still in cinemas. See
		// the descriptor comment.
		{ProviderTheTVDB, LibraryKindMovie, false},
		{ProviderTheTVDB, LibraryKindAdult, false},
		{"", LibraryKindMovie, false},
	}
	for _, c := range cases {
		if got := ProviderServes(c.id, c.kind); got != c.want {
			t.Errorf("ProviderServes(%q, %q) = %v, want %v", c.id, c.kind, got, c.want)
		}
	}
}

// Registering a second television provider must not change which one a library
// gets when nobody chose: migration 0022 backfilled every pre-existing tv row
// onto TMDB, and moving the default would make the create form disagree with
// the rows already on disk.
func TestDefaultTVProviderStaysTMDB(t *testing.T) {
	if got := DefaultProviderForKind(LibraryKindTV); got != ProviderTMDB {
		t.Errorf("DefaultProviderForKind(tv) = %q, want %q", got, ProviderTMDB)
	}
}

func TestDefaultProviderForUnknownKind(t *testing.T) {
	if got := DefaultProviderForKind("music"); got != "" {
		t.Errorf("DefaultProviderForKind(unknown) = %q, want empty", got)
	}
}

// Base parsing is total: it answers for ids validation will go on to refuse, so
// that no caller has to ask the two questions in a particular order.
func TestProviderBase(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		ProviderTMDB:       ProviderTMDB,
		"stashbox:stashdb": ProviderStashbox,
		// An empty slug is not a valid id, and the base of it is still the base.
		"stashbox:": ProviderStashbox,
		// Everything after the FIRST colon is the slug, so a second colon makes
		// the slug invalid rather than making a third level of id.
		"a:b:c": "a",
	}
	for id, want := range cases {
		if got := ProviderBase(id); got != want {
			t.Errorf("ProviderBase(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestValidProviderInstanceID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
		why  string
	}{
		{ProviderStashbox, true, "the legacy instance keeps the bare id forever"},
		{ProviderTMDB, true, "a bare compiled id"},
		{"stashbox:stashdb", true, "base plus slug"},
		{"stashbox:s", true, "a one-character slug"},
		{"stashbox:pmv-stash", true, "dashes inside the slug"},
		{"stashbox:0", true, "a slug may start with a digit"},
		{"", false, "the empty id names no provider"},
		{"bogus", false, "an id no descriptor claims"},
		{"bogus:x", false, "an unknown base cannot be instanced"},
		{"stashbox:", false, "an empty slug"},
		{"stashbox:StashDB", false, "uppercase — ids two rows can disagree about"},
		{"stashbox:-lead", false, "a leading dash"},
		{"stashbox:a:b", false, "a slug cannot contain the separator"},
		{"stashbox:" + strings.Repeat("a", 32), true, "32 characters is the cap"},
		{"stashbox:" + strings.Repeat("a", 33), false, "33 characters is over it"},
		// Deliberate: TMDB's base is real and nothing mints TMDB instances, so
		// this can only be a mistake or a hand-edited chain. Admitting it would
		// put an id in a chain that no registry lookup can resolve to a client.
		{"tmdb:x", false, "only stash-box is instanced"},
		{"anilist:x", false, "only stash-box is instanced"},
	}
	for _, c := range cases {
		if got := ValidProviderInstanceID(c.id); got != c.want {
			t.Errorf("ValidProviderInstanceID(%q) = %v, want %v (%s)", c.id, got, c.want, c.why)
		}
	}
}

// Every slug the deriver produces must be one the validator accepts, or the
// create form mints ids the chain editor then refuses.
func TestProviderSlug(t *testing.T) {
	cases := map[string]string{
		"StashDB":                 "stashdb",
		"PMV Stash":               "pmv-stash",
		"  ThePornDB  ":           "theporndb",
		"My  Box":                 "my-box",
		"box.example":             "box-example",
		"--leading":               "leading",
		"trailing--":              "trailing",
		"":                        "",
		"日本":                      "",
		strings.Repeat("a", 40):   strings.Repeat("a", 32),
		strings.Repeat("ab ", 12): "ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab",
	}
	for name, want := range cases {
		got := ProviderSlug(name)
		if got != want {
			t.Errorf("ProviderSlug(%q) = %q, want %q", name, got, want)
			continue
		}
		if got == "" {
			continue
		}
		if !ValidProviderInstanceID(ProviderStashbox + ":" + got) {
			t.Errorf("ProviderSlug(%q) = %q, which the validator refuses", name, got)
		}
	}
}

// Providers must hand back a copy: a caller sorting or truncating the list for
// display must not be able to reorder the registry underneath everyone else.
func TestProvidersReturnsACopy(t *testing.T) {
	first := Providers()
	first[0].ID = "mutated"
	if again := Providers(); again[0].ID == "mutated" {
		t.Error("Providers() exposed the backing slice")
	}
}
