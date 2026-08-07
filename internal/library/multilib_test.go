package library

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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

	sr, err := h.mgr.AddSeries(ctx, core.TMDBRef(42), nil, anime.ID)
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
	again, err := h.mgr.AddSeries(ctx, core.TMDBRef(42), nil, def.ID)
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

// An add is fetched through the provider its REF names, not through the
// library's choice: the id is written in one provider's vocabulary, and the
// ref is the only thing that says which. The row it writes carries that
// identity, so the refresh below can find its way back.
func TestAddResolvesTheRefsOwnProvider(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	other := &stubProvider{
		seriesByID: map[int64]core.SeriesMeta{
			99: {Provider: "other", ProviderRef: "99", Title: "Frieren", Year: 2023},
		},
	}
	reg := &fakeRegistry{metadata: map[string]core.MetadataProvider{"other": other}}
	h.mgr.providers = reg

	anime := &core.Library{Kind: core.LibraryKindTV, Name: "Anime",
		RootPath: "library/Anime", Provider: "other"}
	if err := h.st.CreateLibrary(ctx, anime); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	sr, err := h.mgr.AddSeries(ctx, core.ItemRef{Provider: "other", Ref: "99"}, nil, anime.ID)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if sr.Title != "Frieren" {
		t.Errorf("added series = %+v, want the registry provider's answer", sr)
	}
	if sr.Provider != "other" || sr.ProviderRef != "99" {
		t.Errorf("added series identity = %q/%q, want the ref it was added by",
			sr.Provider, sr.ProviderRef)
	}
	if len(reg.asked) == 0 || reg.asked[len(reg.asked)-1] != "other" {
		t.Errorf("registry asked for %v, want the ref's own provider id", reg.asked)
	}
}

// A refresh re-fetches every item through the provider that identified it, by
// that provider's own ref. The TMDB fallback must never see the request: it
// would not fail, it would answer about a different show and write it over the
// row.
func TestRefreshFetchesThroughThePinnedProvider(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	other := &stubProvider{
		seriesByID: map[int64]core.SeriesMeta{
			99: {Provider: "other", ProviderRef: "99", Title: "Frieren", Year: 2023, Status: "Ended"},
		},
	}
	reg := &fakeRegistry{metadata: map[string]core.MetadataProvider{"other": other}}
	h.mgr.providers = reg
	// The fallback holds a DIFFERENT show under the same numeric id, so a
	// refresh that reached for it would be visible in the row rather than in a
	// call count.
	h.provider.seriesByID[99] = core.SeriesMeta{TMDBID: 99, Title: "Some Live-Action Show", Year: 2011}

	sr, err := h.mgr.AddSeries(ctx, core.ItemRef{Provider: "other", Ref: "99"}, nil, 0)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	res, err := h.mgr.RefreshLibrary(ctx)
	if err != nil {
		t.Fatalf("RefreshLibrary: %v", err)
	}
	if res.Series != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v, want the one series refreshed", res)
	}
	got, err := h.st.GetSeries(ctx, sr.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got.Title != "Frieren" || got.Status != "Ended" {
		t.Errorf("refreshed series = %+v, want the pinned provider's answer", got)
	}
	for _, id := range reg.asked {
		if id == core.ProviderTMDB {
			t.Errorf("registry asked for %v, want the pinned id only", reg.asked)
			break
		}
	}
}

// An item pinned to a provider that is not configured is one recorded error.
// The sweep continues past it, exactly as it continues past a provider that
// answered badly: the other two hundred titles still get their dates.
func TestRefreshRecordsAnUnconfiguredProviderAndContinues(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.mgr.providers = &fakeRegistry{metadata: map[string]core.MetadataProvider{}}
	h.provider.seriesByID[1396] = core.SeriesMeta{TMDBID: 1396, Title: "Breaking Bad", Year: 2008}

	orphan := &core.Series{Provider: "gone", ProviderRef: "21", Title: "Orphan", Monitored: true}
	if err := h.st.UpsertSeries(ctx, orphan); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if _, err := h.mgr.AddSeries(ctx, core.TMDBRef(1396), nil, 0); err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	res, err := h.mgr.RefreshLibrary(ctx)
	if err != nil {
		t.Fatalf("RefreshLibrary: %v", err)
	}
	if res.Series != 1 || len(res.Errors) != 1 {
		t.Fatalf("result = %+v, want one refreshed and one recorded failure", res)
	}
	if !strings.Contains(res.Errors[0], `provider "gone" is not configured`) {
		t.Errorf("error = %q, want it to name the missing provider", res.Errors[0])
	}
}

// chainHarness builds a two-provider chain over a fresh Anime library: ids
// "first" and "second", in that order, resolved through a fake registry.
func chainHarness(t *testing.T, first, second *stubProvider) (*harness, *core.Library) {
	t.Helper()
	h := newHarness(t)
	h.mgr.providers = &fakeRegistry{metadata: map[string]core.MetadataProvider{
		"first": first, "second": second,
	}}
	anime := &core.Library{Kind: core.LibraryKindTV, Name: "Anime",
		RootPath: "library/Anime", Providers: []string{"first", "second"}}
	if err := h.st.CreateLibrary(context.Background(), anime); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	return h, anime
}

// chainSeries is one provider's whole answer about the fixture show: the
// search hit and the detail GetSeries returns for it.
func chainSeries(provider, ref, title string) *stubProvider {
	hit := core.SeriesMeta{Provider: provider, ProviderRef: ref, Title: title, Year: 2023}
	full := hit
	full.Seasons = []core.SeasonMeta{{
		Number:   1,
		Title:    "Season 1",
		Episodes: []core.EpisodeMeta{{Season: 1, Number: 1, Title: "The Journey's End"}},
	}}
	return &stubProvider{
		series:     []core.SeriesMeta{hit},
		seriesByID: map[int64]core.SeriesMeta{stubRefID(ref): full},
	}
}

// A scan walks the library's chain in order and imports the FIRST provider
// that is confident about the file. A provider that errors is recorded and the
// walk goes on; only a chain where every provider errored parks the file as a
// provider failure, and a chain that answered without recognizing the title
// parks it as no match.
func TestScanWalksTheProviderChain(t *testing.T) {
	rel := "library/Anime/Frieren.S01E01.1080p.WEB-DL.x265.mkv"
	organized := "library/Anime/Frieren (2023)/Season 01/Frieren (2023) - S01E01 - The Journey's End.mkv"

	t.Run("past a provider that errored", func(t *testing.T) {
		broken := &stubProvider{searchErr: errors.New("upstream is down")}
		h, _ := chainHarness(t, broken, chainSeries("second", "7", "Frieren"))
		h.parser[filepath.Base(rel)] = episodeParse("Frieren", 1, 1)
		h.writeVideo(rel, "episode bytes")

		res := h.scan()
		if res.Added != 1 || res.Unmatched != 0 {
			t.Fatalf("result = %+v, want the second provider's match imported", res)
		}
		if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], `"first"`) {
			t.Errorf("errors = %v, want the failed provider named", res.Errors)
		}
		if got := h.read(organized); got != "episode bytes" {
			t.Fatalf("organized file %q missing (content %q)", organized, got)
		}
	})

	t.Run("past a provider that did not recognize it", func(t *testing.T) {
		stranger := chainSeries("first", "1", "A Completely Different Show")
		h, _ := chainHarness(t, stranger, chainSeries("second", "7", "Frieren"))
		h.parser[filepath.Base(rel)] = episodeParse("Frieren", 1, 1)
		h.writeVideo(rel, "episode bytes")

		res := h.scan()
		if res.Added != 1 || res.Unmatched != 0 || len(res.Errors) != 0 {
			t.Fatalf("result = %+v, want the second provider's match imported", res)
		}
		sr, err := h.st.GetSeriesByProviderRef(context.Background(), "second", "7")
		if err != nil {
			t.Fatalf("GetSeriesByProviderRef: %v", err)
		}
		if sr.Title != "Frieren" {
			t.Errorf("series = %+v, want the provider that matched", sr)
		}
	})

	t.Run("every provider errored", func(t *testing.T) {
		down := func() *stubProvider { return &stubProvider{searchErr: errors.New("upstream is down")} }
		h, _ := chainHarness(t, down(), down())
		h.parser[filepath.Base(rel)] = episodeParse("Frieren", 1, 1)
		h.writeVideo(rel, "episode bytes")

		res := h.scan()
		if res.Unmatched != 1 || res.Added != 0 {
			t.Fatalf("result = %+v, want the file parked", res)
		}
		parked := h.unmatched()
		if len(parked) != 1 || parked[0].Reason != reasonProviderErr {
			t.Errorf("unmatched queue = %+v, want %q", parked, reasonProviderErr)
		}
	})
}

// A search asks every provider on the chain and MERGES what they say, in chain
// order. Neither provider's answer may hide the other's: TMDB answers
// something for nearly every anime query, and the anime provider answers
// nothing about the live-action show that shares the name.
func TestSearchLibraryMergesTheChainInOrder(t *testing.T) {
	h, lib := chainHarness(t,
		chainSeries("first", "1", "Frieren"),
		chainSeries("second", "7", "Frieren: Beyond Journey's End"))

	hits, err := h.mgr.SearchLibrary(context.Background(), lib.ID, core.MediaTypeSeries, "frieren")
	if err != nil {
		t.Fatalf("SearchLibrary: %v", err)
	}
	if len(hits.Series) != 2 ||
		hits.Series[0].Provider != "first" || hits.Series[1].Provider != "second" {
		t.Fatalf("hits = %+v, want both providers' answers in chain order", hits.Series)
	}
	if len(hits.Providers) != 2 || hits.Providers[0] != "first" || hits.Providers[1] != "second" {
		t.Errorf("providers = %v, want the chain that ran", hits.Providers)
	}
	if len(hits.Failures) != 0 {
		t.Errorf("failures = %+v, want none", hits.Failures)
	}
}

// One provider being down is a Failure the caller can render, not an error:
// the rest of the chain still answered, and hiding those hits would make a
// half-configured install look like an empty one.
func TestSearchLibrarySurfacesOneProviderFailure(t *testing.T) {
	h, lib := chainHarness(t,
		&stubProvider{searchErr: errors.New("upstream is down")},
		chainSeries("second", "7", "Frieren"))

	hits, err := h.mgr.SearchLibrary(context.Background(), lib.ID, core.MediaTypeSeries, "frieren")
	if err != nil {
		t.Fatalf("SearchLibrary: %v", err)
	}
	if len(hits.Series) != 1 || hits.Series[0].Provider != "second" {
		t.Fatalf("hits = %+v, want the healthy provider's answer", hits.Series)
	}
	if len(hits.Failures) != 1 || hits.Failures[0].Provider != "first" ||
		!strings.Contains(hits.Failures[0].Message, "upstream is down") {
		t.Errorf("failures = %+v, want the down provider named with its reason", hits.Failures)
	}
}

// A chain where EVERY provider failed is an error, and it is the FIRST
// failure's error: a TMDB-headed chain with a rejected key has to surface as
// the credential fault the add dialog explains, not as whatever the provider
// behind it said.
func TestSearchLibraryAllFailedReturnsTheFirstError(t *testing.T) {
	h, lib := chainHarness(t,
		&stubProvider{searchErr: fmt.Errorf("tmdb: %w", core.ErrMetadataUnauthorized)},
		&stubProvider{searchErr: errors.New("upstream is down")})

	hits, err := h.mgr.SearchLibrary(context.Background(), lib.ID, core.MediaTypeSeries, "frieren")
	if hits != nil {
		t.Fatalf("hits = %+v, want none when the whole chain failed", hits)
	}
	if !errors.Is(err, core.ErrMetadataUnauthorized) {
		t.Errorf("error = %v, want the first failure's credential fault", err)
	}
}
