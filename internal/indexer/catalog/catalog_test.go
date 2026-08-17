package catalog

import (
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestAllIncludesEveryKindAndKnownPresets(t *testing.T) {
	all := All()
	if len(all) < 20 {
		t.Fatalf("catalog has %d definitions, want the curated native/generic list", len(all))
	}

	got := map[string]Definition{}
	kinds := map[string]int{}
	for _, def := range all {
		if _, dup := got[def.ID]; dup {
			t.Fatalf("duplicate catalog id %q", def.ID)
		}
		got[def.ID] = def
		kinds[def.Kind]++
	}

	for _, id := range []string{"nzbgeek", "dognzb", "generic-newznab", "generic-torznab", "jackett", "prowlarr", "animetosho", "thepiratebay", "nyaa", "rutor", "tokyotosho"} {
		if _, ok := got[id]; !ok {
			t.Fatalf("catalog missing %q", id)
		}
	}

	if kinds[KindUsenet] < 10 {
		t.Fatalf("usenet definitions = %d, want a comprehensive Newznab list", kinds[KindUsenet])
	}
	if kinds[KindTorrent] < 1 {
		t.Fatalf("torrent definitions = %d, want at least one native Torznab source", kinds[KindTorrent])
	}
	if kinds[KindGeneric] < 3 {
		t.Fatalf("generic definitions = %d, want Jackett/Prowlarr/Torznab/Newznab", kinds[KindGeneric])
	}
}

func TestLookupAndByKind(t *testing.T) {
	geek, ok := Lookup("nzbgeek")
	if !ok {
		t.Fatal("Lookup(nzbgeek) = false")
	}
	if geek.Kind != KindUsenet || geek.Protocol != core.IndexerTypeNewznab {
		t.Fatalf("nzbgeek = %+v, want usenet/newznab", geek)
	}
	if geek.URL != "https://api.nzbgeek.info" {
		t.Fatalf("nzbgeek URL = %q", geek.URL)
	}
	if !geek.RequiresAPIKey {
		t.Fatal("nzbgeek should require an API key")
	}

	if _, ok := Lookup("no-such-indexer"); ok {
		t.Fatal("Lookup(unknown) = true")
	}

	usenet := ByKind(KindUsenet)
	if len(usenet) == 0 {
		t.Fatal("ByKind(usenet) is empty")
	}
	for _, def := range usenet {
		if def.Kind != KindUsenet {
			t.Fatalf("ByKind leaked %q (%s)", def.ID, def.Kind)
		}
	}

	if got := ByKind("nope"); got != nil {
		t.Fatalf("ByKind(unknown) = %v, want nil", got)
	}
}

func TestSearchMatchesNameDescriptionAndID(t *testing.T) {
	hits := Search(KindUsenet, "geek")
	if len(hits) == 0 || hits[0].ID != "nzbgeek" {
		t.Fatalf("Search(usenet, geek) = %+v, want nzbgeek first", ids(hits))
	}

	if torrents := Search(KindTorrent, "1337"); len(torrents) != 0 {
		t.Fatalf("Search(torrent, 1337) = %v, want no homepage-only definitions", ids(torrents))
	}

	if got := Search(KindGeneric, "   "); len(got) != len(ByKind(KindGeneric)) {
		t.Fatalf("blank search should return the whole kind, got %d want %d", len(got), len(ByKind(KindGeneric)))
	}
}

func TestDefinitionsAreSaveable(t *testing.T) {
	for _, def := range All() {
		if strings.TrimSpace(def.ID) == "" || strings.TrimSpace(def.Name) == "" {
			t.Fatalf("definition missing id/name: %+v", def)
		}
		if def.Kind != KindTorrent && def.Kind != KindUsenet && def.Kind != KindGeneric {
			t.Fatalf("%s has unknown kind %q", def.ID, def.Kind)
		}
		if def.Protocol != core.IndexerTypeTorznab && def.Protocol != core.IndexerTypeNewznab {
			t.Fatalf("%s has unknown protocol %q", def.ID, def.Protocol)
		}
		if def.URL != "" && !strings.HasPrefix(def.URL, "http://") && !strings.HasPrefix(def.URL, "https://") {
			t.Fatalf("%s URL %q is not http(s)", def.ID, def.URL)
		}
		if def.Kind != KindGeneric && strings.TrimSpace(def.URL) == "" {
			t.Fatalf("%s is a named %s preset without an explicit API URL", def.ID, def.Kind)
		}
		if def.DefinitionID != "" && def.Protocol != core.IndexerTypeTorznab {
			t.Fatalf("%s local definition is not Torznab", def.ID)
		}
		if def.URLPlaceholder != "" && !strings.HasPrefix(def.URLPlaceholder, "http://") && !strings.HasPrefix(def.URLPlaceholder, "https://") {
			t.Fatalf("%s placeholder %q is not an http(s) feed endpoint", def.ID, def.URLPlaceholder)
		}
		for _, cat := range def.Categories {
			if cat <= 0 {
				t.Fatalf("%s has non-positive category %d", def.ID, cat)
			}
		}
		for _, tag := range def.Content {
			switch tag {
			case ContentMovies, ContentTV, ContentAnime, ContentAudio,
				ContentBooks, ContentAdult, ContentPC, ContentOther:
			default:
				t.Fatalf("%s has unknown content tag %q", def.ID, tag)
			}
		}
	}
}

func TestCatalogContainsOnlyDirectlyConfigurableNamedSources(t *testing.T) {
	if _, ok := Lookup("1337x"); ok {
		t.Fatal("homepage-only 1337x must not be exposed as an addable Torznab preset")
	}
	tpb, ok := Lookup("thepiratebay")
	if !ok || tpb.DefinitionID != "thepiratebay" {
		t.Fatalf("The Pirate Bay local definition = %+v, found=%v", tpb, ok)
	}
	for _, id := range []string{"nyaa", "rutor", "tokyotosho"} {
		def, ok := Lookup(id)
		if !ok || def.DefinitionID != id || def.Privacy != PrivacyPublic {
			t.Fatalf("%s local definition = %+v, found=%v", id, def, ok)
		}
	}

	geek, ok := Lookup("nzbgeek")
	if !ok {
		t.Fatal("Lookup(nzbgeek) = false")
	}
	if geek.Privacy != PrivacyPrivate {
		t.Fatalf("nzbgeek privacy = %s", geek.Privacy)
	}
	if !hasAll(geek.Content, ContentMovies, ContentTV) {
		t.Fatalf("nzbgeek content = %v, want movies/tv from its Newznab blocks", geek.Content)
	}
	if !geek.RequiresAPIKey {
		t.Fatal("nzbgeek should require an API key")
	}

	tosho, ok := Lookup("animetosho")
	if !ok {
		t.Fatal("Lookup(animetosho) = false")
	}
	if !hasAll(tosho.Content, ContentAnime) {
		t.Fatalf("animetosho content = %v, want anime", tosho.Content)
	}
	if tosho.URL != "https://feed.animetosho.org" {
		t.Fatalf("animetosho URL = %q, want its native feed host", tosho.URL)
	}
}

func hasAll(have []string, want ...string) bool {
	set := map[string]bool{}
	for _, tag := range have {
		set[tag] = true
	}
	for _, tag := range want {
		if !set[tag] {
			return false
		}
	}
	return true
}

func ids(defs []Definition) []string {
	out := make([]string, len(defs))
	for i, def := range defs {
		out[i] = def.ID
	}
	return out
}
