package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/watzon/caravan/internal/api"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/indexer"
	"github.com/watzon/caravan/internal/store"
)

// fakeTorznab is a Torznab endpoint that logs the query parameters of every
// request it serves. The tests below drive it through the real
// internal/indexer client, so what they assert is the `cat` parameter that
// actually goes out on the wire rather than an argument handed to a stub.
//
// An indexer's identity is the name in its URL path, so one server backs any
// number of configured indexers.
type fakeTorznab struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []torznabRequest
}

type torznabRequest struct {
	indexer string
	mode    string
	cats    string
}

func startFakeTorznab(t *testing.T) *fakeTorznab {
	t.Helper()
	f := &fakeTorznab{}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{name}/api", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, torznabRequest{
			indexer: r.PathValue("name"),
			mode:    r.URL.Query().Get("t"),
			cats:    r.URL.Query().Get("cat"),
		})
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel></channel></rss>`)
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeTorznab) url(name string) string { return f.server.URL + "/" + name }

// factory builds real indexer clients against this server.
func (f *fakeTorznab) factory() api.IndexerFactory {
	return func(cfg core.IndexerConfig) api.IndexerClient {
		return indexer.New(cfg, f.server.Client())
	}
}

// recorded returns the request log sorted, since a search fan-out is
// concurrent and the order indexers answer in is not part of the contract.
func (f *fakeTorznab) recorded() []torznabRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]torznabRequest(nil), f.requests...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].indexer != out[j].indexer {
			return out[i].indexer < out[j].indexer
		}
		if out[i].mode != out[j].mode {
			return out[i].mode < out[j].mode
		}
		return out[i].cats < out[j].cats
	})
	return out
}

func (f *fakeTorznab) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = nil
}

func formatRequests(requests []torznabRequest) string {
	parts := make([]string, 0, len(requests))
	for _, req := range requests {
		parts = append(parts, fmt.Sprintf("%s t=%s cat=%q", req.indexer, req.mode, req.cats))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func addTorznabIndexer(t *testing.T, ctx context.Context, st *store.Store, fake *fakeTorznab, name string, cats ...int) core.IndexerConfig {
	t.Helper()
	cfg := core.IndexerConfig{Name: name, URL: fake.url(name), Enabled: true, Categories: cats}
	if err := st.UpsertIndexer(ctx, &cfg); err != nil {
		t.Fatalf("upsert indexer %q: %v", name, err)
	}
	return cfg
}

// overrideLibraryIndexer writes one per-library indexer override, addressing
// the library the way items do: by kind.
func overrideLibraryIndexer(t *testing.T, ctx context.Context, st *store.Store, kind string, indexerID int64, enabled bool, cats []int) {
	t.Helper()
	library, err := st.GetLibraryByKind(ctx, kind)
	if err != nil {
		t.Fatalf("get %s library: %v", kind, err)
	}
	if err := st.SetLibraryIndexer(ctx, &core.LibraryIndexer{
		LibraryID: library.ID, IndexerID: indexerID, Enabled: enabled, Categories: cats,
	}); err != nil {
		t.Fatalf("set %s library indexer: %v", kind, err)
	}
}

func addEpisode(t *testing.T, ctx context.Context, st *store.Store, title string) core.Episode {
	t.Helper()
	series := core.Series{TMDBID: 42, Title: title, SortTitle: title, Year: 2016, Monitored: true}
	if err := st.UpsertSeries(ctx, &series); err != nil {
		t.Fatalf("upsert series: %v", err)
	}
	episode := core.Episode{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 1, Monitored: true}
	if err := st.UpsertEpisode(ctx, &episode); err != nil {
		t.Fatalf("upsert episode: %v", err)
	}
	return episode
}

func searchMovieJob(t *testing.T, ctx context.Context, runner *Runner, st *store.Store, movieID int64) {
	t.Helper()
	payload, _ := json.Marshal(core.JobSearchMoviePayload{MovieID: movieID})
	if err := runner.handleSearchMovie(ctx, st, payload); err != nil {
		t.Fatalf("handle search movie: %v", err)
	}
}

func searchEpisodeJob(t *testing.T, ctx context.Context, runner *Runner, st *store.Store, episodeID int64) {
	t.Helper()
	payload, _ := json.Marshal(core.JobSearchEpisodePayload{EpisodeID: episodeID})
	if err := runner.handleSearchEpisode(ctx, st, payload); err != nil {
		t.Fatalf("handle search episode: %v", err)
	}
}

// A search must carry the categories of the library the searched item belongs
// to, and only those: a movie search that sends the TV categories asks every
// indexer for television and calls the empty answer "no releases found".
func TestSearchSendsOnlyItsLibrarysCategories(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	fake := startFakeTorznab(t)
	cfg := addTorznabIndexer(t, ctx, st, fake, "shared", 2000, 5000)
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindMovie, cfg.ID, true, []int{2000})
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindTV, cfg.ID, true, []int{5000})

	movie := addMovie(t, ctx, st, "Example Movie", 2024, true)
	episode := addEpisode(t, ctx, st, "Example Series")
	runner := NewRunner(st, fake.factory(), func(context.Context, string) core.Engine { return &fakeEngine{} })

	searchMovieJob(t, ctx, runner, st, movie.ID)
	searchEpisodeJob(t, ctx, runner, st, episode.ID)

	got := fake.recorded()
	want := []torznabRequest{
		{indexer: "shared", mode: "movie", cats: "2000"},
		{indexer: "shared", mode: "tvsearch", cats: "5000"},
	}
	if len(got) != len(want) {
		t.Fatalf("torznab requests = %s, want one movie and one tv search", formatRequests(got))
	}
	for i, req := range want {
		if got[i] != req {
			t.Fatalf("torznab requests = %s, want %s", formatRequests(got), formatRequests(want))
		}
	}
	for _, req := range got {
		if req.mode == "movie" && strings.Contains(req.cats, "5000") {
			t.Fatalf("movie search asked for the TV categories: %s", formatRequests(got))
		}
	}
}

// Disabling an indexer for one library removes it from that library's searches
// and from nothing else. Without the per-library row it was all or nothing:
// the only way to stop asking a movie-only tracker for television was to turn
// it off for movies too.
func TestPerLibraryIndexerDisableLeavesOtherLibrariesUnaffected(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	fake := startFakeTorznab(t)
	addTorznabIndexer(t, ctx, st, fake, "alpha", 2000, 5000)
	beta := addTorznabIndexer(t, ctx, st, fake, "beta", 2000, 5000)
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindTV, beta.ID, false, nil)

	movie := addMovie(t, ctx, st, "Example Movie", 2024, true)
	episode := addEpisode(t, ctx, st, "Example Series")
	runner := NewRunner(st, fake.factory(), func(context.Context, string) core.Engine { return &fakeEngine{} })

	searchMovieJob(t, ctx, runner, st, movie.ID)
	movieRequests := fake.recorded()
	if len(movieRequests) != 2 || movieRequests[0].indexer != "alpha" || movieRequests[1].indexer != "beta" {
		t.Fatalf("movie search hit %s, want both indexers", formatRequests(movieRequests))
	}

	fake.reset()
	searchEpisodeJob(t, ctx, runner, st, episode.ID)
	episodeRequests := fake.recorded()
	if len(episodeRequests) != 1 || episodeRequests[0].indexer != "alpha" {
		t.Fatalf("episode search hit %s, want only the indexer the tv library still enables", formatRequests(episodeRequests))
	}

	// The globally enabled indexer is untouched: only the pair was disabled.
	enabled, err := st.ListEnabledIndexers(ctx)
	if err != nil {
		t.Fatalf("list enabled indexers: %v", err)
	}
	if len(enabled) != 2 {
		t.Fatalf("enabled indexers = %d, want the per-library row to have left the indexer itself enabled", len(enabled))
	}
}

// An RSS feed is a firehose of everything new, so it is fetched once per
// indexer per cycle no matter how many libraries subscribe to it — asking
// twice fetches the same document twice and spends a rate limit for nothing.
// The one request carries the union of what the libraries asked for, because
// narrowing it to either would drop releases the other is entitled to see.
func TestRSSSyncFetchesEachIndexerOncePerCycleWithTheCategoryUnion(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	fake := startFakeTorznab(t)
	cfg := addTorznabIndexer(t, ctx, st, fake, "shared", 2000, 5000)
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindMovie, cfg.ID, true, []int{2000})
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindTV, cfg.ID, true, []int{5000})

	addMovie(t, ctx, st, "Example Movie", 2024, true)
	addEpisode(t, ctx, st, "Example Series")
	runner := NewRunner(st, fake.factory(), func(context.Context, string) core.Engine { return &fakeEngine{} })

	if err := runner.handleRSSSync(ctx, st, json.RawMessage("{}")); err != nil {
		t.Fatalf("handle rss sync: %v", err)
	}

	got := fake.recorded()
	if len(got) != 1 {
		t.Fatalf("rss cycle made %s, want exactly one fetch per enabled indexer", formatRequests(got))
	}
	if got[0].mode != "search" || got[0].cats != "2000,5000" {
		t.Fatalf("rss fetch = %s, want one t=search carrying the category union", formatRequests(got))
	}
}

// A library that asked for everything is not narrowed by another library's
// category list: the union of "all" and "2000" is "all".
func TestRSSSyncUnionOfUnfilteredLibraryIsUnfiltered(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	fake := startFakeTorznab(t)
	cfg := addTorznabIndexer(t, ctx, st, fake, "shared", 2000)
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindTV, cfg.ID, true, []int{})

	runner := NewRunner(st, fake.factory(), func(context.Context, string) core.Engine { return &fakeEngine{} })
	if err := runner.handleRSSSync(ctx, st, json.RawMessage("{}")); err != nil {
		t.Fatalf("handle rss sync: %v", err)
	}

	got := fake.recorded()
	if len(got) != 1 || got[0].cats != "" {
		t.Fatalf("rss fetch = %s, want one unfiltered fetch", formatRequests(got))
	}
}

// The shared fetch must not become a shared decision: a release from an
// indexer only one library enabled may only satisfy that library's items.
func TestRSSSyncMatchesOnlyLibrariesThatEnabledTheIndexer(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	addIndexer(t, ctx, st)
	indexers, err := st.ListEnabledIndexers(ctx)
	if err != nil {
		t.Fatalf("list enabled indexers: %v", err)
	}
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindTV, indexers[0].ID, false, nil)

	addMovie(t, ctx, st, "Example Movie", 2024, true)
	addEpisode(t, ctx, st, "Example Series")
	fakeClient := &fakeIndexer{rss: []core.Release{
		{
			GUID: "m", Title: "Example Movie 2024 1080p", Protocol: core.ProtocolTorrent, Seeders: 5,
			Parsed: core.ParsedRelease{Title: "Example Movie", Year: 2024, Quality: core.Quality1080p, Source: core.SourceWebDL},
		},
		{
			GUID: "e", Title: "Example Series S01E01 1080p", Protocol: core.ProtocolTorrent, Seeders: 5,
			Parsed: core.ParsedRelease{
				Title: "Example Series", Season: 1, Episodes: []int{1},
				Quality: core.Quality1080p, Source: core.SourceWebDL,
			},
		},
	}}
	engine := &fakeEngine{}
	runner := newRunner(st, fakeClient, engine)

	if err := runner.handleRSSSync(ctx, st, json.RawMessage("{}")); err != nil {
		t.Fatalf("handle rss sync: %v", err)
	}
	if len(engine.added) != 1 || engine.added[0].GUID != "m" {
		t.Fatalf("grabbed %+v, want only the movie: the tv library does not search this indexer", engine.added)
	}
}

// The library's default quality profile has to reach the decision, not just the
// database. An episode whose series names no profile is graded against the tv
// library's default, so a release the store-wide default would have accepted is
// rejected once the library asks for better.
func TestAutomaticSearchGradesAgainstTheLibraryDefaultProfile(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	addIndexer(t, ctx, st)

	// The seeded store-wide default accepts 720p; this one does not.
	hd := core.QualityProfile{Name: "HD only", Cutoff: core.Quality1080p, Items: []string{core.Quality1080p}}
	if err := st.CreateQualityProfile(ctx, &hd); err != nil {
		t.Fatalf("create quality profile: %v", err)
	}
	tv, err := st.GetLibraryByKind(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("get tv library: %v", err)
	}
	tv.QualityProfileID = hd.ID
	if err := st.UpdateLibrary(ctx, tv); err != nil {
		t.Fatalf("update tv library: %v", err)
	}

	episode := addEpisode(t, ctx, st, "Example Series")
	movie := addMovie(t, ctx, st, "Example Movie", 2024, true)
	indexer := &fakeIndexer{
		tv: []core.Release{{
			GUID: "e720", Title: "Example Series S01E01 720p", Protocol: core.ProtocolTorrent, Seeders: 10,
			Parsed: core.ParsedRelease{
				Title: "Example Series", Season: 1, Episodes: []int{1},
				Quality: core.Quality720p, Source: core.SourceWebDL,
			},
		}},
		movies: []core.Release{{
			GUID: "m720", Title: "Example Movie 2024 720p", Protocol: core.ProtocolTorrent, Seeders: 10,
			Parsed: core.ParsedRelease{
				Title: "Example Movie", Year: 2024,
				Quality: core.Quality720p, Source: core.SourceWebDL,
			},
		}},
	}
	engine := &fakeEngine{}
	runner := newRunner(st, indexer, engine)

	searchEpisodeJob(t, ctx, runner, st, episode.ID)
	if len(engine.added) != 0 {
		t.Fatalf("grabbed %+v, want nothing: the tv library defaults to a profile that rejects 720p", engine.added)
	}

	// The movie library set no default, so its items still resolve to the
	// store-wide one and the identical release is accepted.
	searchMovieJob(t, ctx, runner, st, movie.ID)
	if len(engine.added) != 1 || engine.added[0].GUID != "m720" {
		t.Fatalf("grabbed %+v, want the movie: only the tv library narrowed its default", engine.added)
	}
}

// A shared fetch must not undo a library's category narrowing. The union that
// made one fetch out of many carries the other libraries' categories too, so
// offering every result to every kind grabs releases the interactive and
// backlog searches for the same item would never have been shown.
func TestRSSSyncDropsReleasesOutsideALibrarysCategories(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	addIndexer(t, ctx, st)
	indexers, err := st.ListEnabledIndexers(ctx)
	if err != nil {
		t.Fatalf("list enabled indexers: %v", err)
	}
	// Movies keeps the whole 2000 and 5000 trees; Series asks for SD only.
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindMovie, indexers[0].ID, true, []int{2000, 5000})
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindTV, indexers[0].ID, true, []int{5030})

	addMovie(t, ctx, st, "Example Movie", 2024, true)
	addEpisode(t, ctx, st, "Example Series")
	engine := &fakeEngine{}
	runner := newRunner(st, &fakeIndexer{rss: []core.Release{
		{
			GUID: "m", Title: "Example Movie 2024 1080p", Protocol: core.ProtocolTorrent, Seeders: 5,
			Categories: []int{2040},
			Parsed:     core.ParsedRelease{Title: "Example Movie", Year: 2024, Quality: core.Quality1080p, Source: core.SourceWebDL},
		},
		{
			// In the 5000 tree the union asked for on the movie library's
			// behalf, but not in the 5030 the tv library narrowed itself to.
			GUID: "e", Title: "Example Series S01E01 1080p", Protocol: core.ProtocolTorrent, Seeders: 5,
			Categories: []int{5040},
			Parsed: core.ParsedRelease{
				Title: "Example Series", Season: 1, Episodes: []int{1},
				Quality: core.Quality1080p, Source: core.SourceWebDL,
			},
		},
	}}, engine)

	if err := runner.handleRSSSync(ctx, st, json.RawMessage("{}")); err != nil {
		t.Fatalf("handle rss sync: %v", err)
	}
	if len(engine.added) != 1 || engine.added[0].GUID != "m" {
		t.Fatalf("grabbed %+v, want only the movie: the tv library never asked for category 5040", engine.added)
	}
}

// The filter can only reject what it can see. An indexer that publishes no
// category on an item leaves nothing to match against, and dropping the item
// would silently narrow every such indexer down to nothing — which is how it
// behaved before there were per-library categories at all.
func TestRSSSyncKeepsReleasesTheIndexerPublishedNoCategoryFor(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	addIndexer(t, ctx, st)
	indexers, err := st.ListEnabledIndexers(ctx)
	if err != nil {
		t.Fatalf("list enabled indexers: %v", err)
	}
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindTV, indexers[0].ID, true, []int{5030})

	addEpisode(t, ctx, st, "Example Series")
	engine := &fakeEngine{}
	runner := newRunner(st, &fakeIndexer{rss: []core.Release{{
		GUID: "e", Title: "Example Series S01E01 1080p", Protocol: core.ProtocolTorrent, Seeders: 5,
		Parsed: core.ParsedRelease{
			Title: "Example Series", Season: 1, Episodes: []int{1},
			Quality: core.Quality1080p, Source: core.SourceWebDL,
		},
	}}}, engine)

	if err := runner.handleRSSSync(ctx, st, json.RawMessage("{}")); err != nil {
		t.Fatalf("handle rss sync: %v", err)
	}
	if len(engine.added) != 1 {
		t.Fatalf("grabbed %+v, want the episode: the indexer published no category to reject it by", engine.added)
	}
}
