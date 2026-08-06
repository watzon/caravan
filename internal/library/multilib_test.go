package library

import (
	"context"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// With several libraries of one kind, a scan must attribute each file to the
// library whose root holds it: a series found under the Anime root belongs to
// the Anime library and organizes under it, while the default TV library keeps
// answering for everything else.
func TestScanAttributesFilesToTheirLibrary(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seedSeries(h)

	anime := &core.Library{Kind: core.LibraryKindTV, Name: "Anime",
		RootPath: "library/Anime", Provider: core.ProviderTMDB}
	if err := h.st.CreateLibrary(ctx, anime); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	raw := "library/Anime/Planet.Earth.II.S01E01.720p.mkv"
	h.parser["Planet.Earth.II.S01E01.720p.mkv"] = episodeParse("Planet Earth II", 1, 1)
	h.writeVideo(raw, "episode bytes")

	res := h.scan()
	if res.Added != 1 || res.Unmatched != 0 || len(res.Errors) != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}

	organized := "library/Anime/Planet Earth II (2016)/Season 01/Planet Earth II (2016) - S01E01 - Islands.mkv"
	if got := h.read(organized); got != "episode bytes" {
		t.Fatalf("organized file %q missing (content %q)", organized, got)
	}

	sr, err := h.st.GetSeriesByTMDBID(ctx, 42)
	if err != nil {
		t.Fatalf("GetSeriesByTMDBID: %v", err)
	}
	if sr.LibraryID != anime.ID {
		t.Errorf("series library_id = %d, want the Anime library %d", sr.LibraryID, anime.ID)
	}
	if sr.Path != "library/Anime/Planet Earth II (2016)" {
		t.Errorf("series path = %q, want it under library/Anime", sr.Path)
	}

	// A rescan is idempotent and does not move the series anywhere.
	res = h.scan()
	if res.Updated != 1 || res.Added != 0 || len(res.Errors) != 0 {
		t.Fatalf("rescan result: %+v", res)
	}
	again, err := h.st.GetSeriesByTMDBID(ctx, 42)
	if err != nil {
		t.Fatalf("GetSeriesByTMDBID after rescan: %v", err)
	}
	if again.LibraryID != anime.ID || again.Path != sr.Path {
		t.Errorf("rescan moved the series: %+v", again)
	}
}

// A video loose under library/ that no library root claims parks in the
// review queue: with several libraries there is no defensible default, and a
// visible park beats a silent misfile as a movie.
func TestScanParksFilesUnderNoLibraryRoot(t *testing.T) {
	h := newHarness(t)
	seedMovie(h)

	loose := "library/Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP.mkv"
	h.parser["Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP.mkv"] = movieParse("Big Buck Bunny", 2008)
	h.writeVideo(loose, "movie bytes")

	res := h.scan()
	if res.Unmatched != 1 || res.Added != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	parked := h.unmatched()
	if len(parked) != 1 || parked[0].Path != loose || parked[0].Reason != reasonNoLibrary {
		t.Errorf("unmatched queue = %+v, want the loose file with %q", parked, reasonNoLibrary)
	}
}

// An add that targets a specific library files the new item under that
// library's root; re-adding it with a different target refreshes in place
// rather than moving it.
func TestAddTargetsTheChosenLibrary(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seedSeries(h)

	anime := &core.Library{Kind: core.LibraryKindTV, Name: "Anime",
		RootPath: "library/Anime", Provider: core.ProviderTMDB}
	if err := h.st.CreateLibrary(ctx, anime); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	sr, err := h.mgr.AddSeries(ctx, 42, nil, anime.ID)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if sr.LibraryID != anime.ID || sr.Path != "library/Anime/Planet Earth II (2016)" {
		t.Errorf("added series = {library %d, path %q}, want the Anime library", sr.LibraryID, sr.Path)
	}

	def, err := h.st.GetDefaultLibrary(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetDefaultLibrary: %v", err)
	}
	again, err := h.mgr.AddSeries(ctx, 42, nil, def.ID)
	if err != nil {
		t.Fatalf("AddSeries(again): %v", err)
	}
	if again.LibraryID != anime.ID || again.Path != sr.Path {
		t.Errorf("re-add moved the series to {library %d, path %q}, want it kept in Anime", again.LibraryID, again.Path)
	}
}

// fakeRegistry answers provider ids with canned providers and records what
// was asked, so a test can prove WHICH library's provider choice served a
// request.
type fakeRegistry struct {
	metadata map[string]core.MetadataProvider
	asked    []string
}

func (f *fakeRegistry) Metadata(_ context.Context, id string) core.MetadataProvider {
	f.asked = append(f.asked, id)
	return f.metadata[id]
}

func (f *fakeRegistry) Adult(_ context.Context, _ string) core.AdultMetadataProvider { return nil }

// A library's provider column names which client refreshes its items: an add
// into a library configured with a second provider id resolves that id
// through the registry, while the default library keeps the fallback.
func TestAddResolvesTheLibrarysOwnProvider(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	other := &stubProvider{
		seriesByID: map[int64]core.SeriesMeta{
			99: {TMDBID: 99, Title: "Frieren", Year: 2023},
		},
	}
	reg := &fakeRegistry{metadata: map[string]core.MetadataProvider{"other": other}}
	h.mgr.providers = reg

	anime := &core.Library{Kind: core.LibraryKindTV, Name: "Anime",
		RootPath: "library/Anime", Provider: "other"}
	if err := h.st.CreateLibrary(ctx, anime); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	sr, err := h.mgr.AddSeries(ctx, 99, nil, anime.ID)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if sr.Title != "Frieren" {
		t.Errorf("added series = %+v, want the registry provider's answer", sr)
	}
	if len(reg.asked) == 0 || reg.asked[len(reg.asked)-1] != "other" {
		t.Errorf("registry asked for %v, want the library's own provider id", reg.asked)
	}
}
