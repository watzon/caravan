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
		{"anilist", LibraryKindTV, false},
		{"", LibraryKindMovie, false},
	}
	for _, c := range cases {
		if got := ProviderServes(c.id, c.kind); got != c.want {
			t.Errorf("ProviderServes(%q, %q) = %v, want %v", c.id, c.kind, got, c.want)
		}
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
