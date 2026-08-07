package core

import "testing"

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

// Providers must hand back a copy: a caller sorting or truncating the list for
// display must not be able to reorder the registry underneath everyone else.
func TestProvidersReturnsACopy(t *testing.T) {
	first := Providers()
	first[0].ID = "mutated"
	if again := Providers(); again[0].ID == "mutated" {
		t.Error("Providers() exposed the backing slice")
	}
}
