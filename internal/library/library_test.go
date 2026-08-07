package library

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// fakeParser drives the library's matching and reconciliation logic with
// deterministic parses. The real parser has its own corpus tests (PLAN phase 1
// task 3); the library's job is what it does with a parse, not how the parse
// was produced.
type fakeParser map[string]core.ParsedRelease

func (f fakeParser) parse(name string) core.ParsedRelease {
	if p, ok := f[name]; ok {
		return p
	}
	if p, ok := parseOrganizedName(name); ok {
		return p
	}
	// Anything else looks like the parser gave up, which is what parks a file
	// in the review queue.
	return core.ParsedRelease{
		Title:      strings.TrimSuffix(name, filepath.Ext(name)),
		Confidence: 0.1,
	}
}

// Recognizers for the names the organizer itself writes. A rescan re-reads its
// own output, so the fake parser has to understand it — and deliberately
// recovers only what a Jellyfin-convention name actually carries: title, year,
// and season/episode numbers. Quality, source, codec, and group are gone once a
// file is renamed, which is exactly the loss preserveKnownTags exists to cover.
var (
	organizedEpisodeRe = regexp.MustCompile(`^(.+) \((\d{4})\) - S(\d{2})((?:E\d{2})(?:-E\d{2})*)(?: - .+)?$`)
	organizedMovieRe   = regexp.MustCompile(`^(.+) \((\d{4})\)(?: - .+)?$`)
	episodeNumberRe    = regexp.MustCompile(`\d{2}`)
)

func parseOrganizedName(name string) (core.ParsedRelease, bool) {
	stem := strings.TrimSuffix(name, filepath.Ext(name))

	if match := organizedEpisodeRe.FindStringSubmatch(stem); match != nil {
		year, _ := strconv.Atoi(match[2])
		season, _ := strconv.Atoi(match[3])
		var episodes []int
		for _, n := range episodeNumberRe.FindAllString(match[4], -1) {
			e, _ := strconv.Atoi(n)
			episodes = append(episodes, e)
		}
		return core.ParsedRelease{
			Title:      match[1],
			Year:       year,
			Season:     season,
			Episodes:   episodes,
			Confidence: 0.9,
		}, true
	}

	if match := organizedMovieRe.FindStringSubmatch(stem); match != nil {
		year, _ := strconv.Atoi(match[2])
		return core.ParsedRelease{Title: match[1], Year: year, Confidence: 0.9}, true
	}
	return core.ParsedRelease{}, false
}

// stubProvider is an in-memory core.MetadataProvider.
type stubProvider struct {
	movies     []core.MovieMeta
	series     []core.SeriesMeta
	seriesByID map[int64]core.SeriesMeta
	movieByID  map[int64]core.MovieMeta

	// searchErr fails the Search* calls, getErr the Get* calls.
	searchErr error
	getErr    error
}

func (s *stubProvider) SearchMovies(_ context.Context, _ string) ([]core.MovieMeta, error) {
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	out := make([]core.MovieMeta, len(s.movies))
	for i, m := range s.movies {
		out[i] = stubMovieMeta(m)
	}
	return out, nil
}

func (s *stubProvider) SearchSeries(_ context.Context, _ string) ([]core.SeriesMeta, error) {
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	out := make([]core.SeriesMeta, len(s.series))
	for i, sr := range s.series {
		out[i] = stubSeriesMeta(sr)
	}
	return out, nil
}

func (s *stubProvider) GetMovie(_ context.Context, ref string) (*core.MovieMeta, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if m, ok := s.movieByID[stubRefID(ref)]; ok {
		m = stubMovieMeta(m)
		return &m, nil
	}
	return nil, errors.New("stub: no such movie")
}

func (s *stubProvider) GetSeries(_ context.Context, ref string) (*core.SeriesMeta, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if sr, ok := s.seriesByID[stubRefID(ref)]; ok {
		sr = stubSeriesMeta(sr)
		return &sr, nil
	}
	return nil, errors.New("stub: no such series")
}

// stubMovieMeta and stubSeriesMeta stamp the provider identity every real
// client puts on every answer it returns (see tmdb.movieMeta), so the fixtures
// below can stay keyed by a bare TMDB id while the code under test reads refs.
// A fixture that names its own provider — one standing in for a second
// registry client — is left exactly as written.
func stubMovieMeta(m core.MovieMeta) core.MovieMeta {
	if m.Provider == "" && m.TMDBID != 0 {
		ref := core.TMDBRef(m.TMDBID)
		m.Provider, m.ProviderRef = ref.Provider, ref.Ref
	}
	return m
}

func stubSeriesMeta(s core.SeriesMeta) core.SeriesMeta {
	if s.Provider == "" && s.TMDBID != 0 {
		ref := core.TMDBRef(s.TMDBID)
		s.Provider, s.ProviderRef = ref.Provider, ref.Ref
	}
	return s
}

// stubRefID parses a TMDB-shaped ref back into the int64 the stub's fixture
// maps are keyed by. An unparsable ref yields 0, which matches nothing — the
// same "no such title" answer the real client's ErrInvalidRef amounts to here.
func stubRefID(ref string) int64 {
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// posterBytes is what the stub image host serves.
var posterBytes = []byte("\xff\xd8\xff\xe0 fake jpeg")

type harness struct {
	t          *testing.T
	root       string
	st         *store.Store
	mgr        *Manager
	provider   *stubProvider
	parser     fakeParser
	posterURL  string
	posterHits int
	// hc is the stub image host's client; every Manager the harness builds
	// uses it so no test can reach the real network.
	hc *http.Client
}

// Stock library rows matching the seeded defaults, for tests that assert the
// paths the builders produce without caring which row they came from.
func stockMovieLib() *core.Library {
	return &core.Library{Kind: core.LibraryKindMovie, RootPath: "library/Movies"}
}
func stockTVLib() *core.Library {
	return &core.Library{Kind: core.LibraryKindTV, RootPath: "library/TV"}
}
func stockAdultLib() *core.Library {
	return &core.Library{Kind: core.LibraryKindAdult, RootPath: "library/Adult"}
}

// newHarness builds a Manager over a temp storage root, a real sqlite store,
// a stub provider, and a stub image host.
func newHarness(t *testing.T) *harness {
	t.Helper()

	root := t.TempDir()
	h := &harness{
		t:        t,
		root:     root,
		provider: &stubProvider{seriesByID: map[int64]core.SeriesMeta{}, movieByID: map[int64]core.MovieMeta{}},
		parser:   fakeParser{},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.posterHits++
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(posterBytes)
	}))
	t.Cleanup(srv.Close)
	h.posterURL = srv.URL + "/poster.jpg"
	h.hc = srv.Client()

	h.st = h.openStore(filepath.Join(t.TempDir(), "caravan.db"))
	h.mgr = h.newManager(h.st, h.provider)
	return h
}

func (h *harness) openStore(path string) *store.Store {
	h.t.Helper()
	st, err := store.Open(path)
	if err != nil {
		h.t.Fatalf("store.Open: %v", err)
	}
	h.t.Cleanup(func() { st.Close() })
	return st
}

// newManager wires a Manager to the harness' fake parser. mp may be nil, which
// is the "no provider configured" case.
func (h *harness) newManager(st *store.Store, mp core.MetadataProvider) *Manager {
	h.t.Helper()
	mgr := NewManager(st, mp, h.root)
	mgr.parse = h.parser.parse
	if h.hc != nil {
		mgr.hc = h.hc
	}
	return mgr
}

// writeVideo creates a video file at a storage-root-relative path.
func (h *harness) writeVideo(rel, content string) {
	h.t.Helper()
	abs := filepath.Join(h.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		h.t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		h.t.Fatalf("write %s: %v", rel, err)
	}
}

func (h *harness) scan() *ScanResult {
	h.t.Helper()
	res, err := h.mgr.Scan(context.Background())
	if err != nil {
		h.t.Fatalf("Scan: %v", err)
	}
	return res
}

func (h *harness) exists(rel string) bool {
	h.t.Helper()
	_, err := os.Stat(filepath.Join(h.root, filepath.FromSlash(rel)))
	return err == nil
}

func (h *harness) read(rel string) string {
	h.t.Helper()
	b, err := os.ReadFile(filepath.Join(h.root, filepath.FromSlash(rel)))
	if err != nil {
		h.t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// movieParse is the parse a well-named movie file yields.
func movieParse(title string, year int) core.ParsedRelease {
	return core.ParsedRelease{
		Title:      title,
		Year:       year,
		Quality:    core.Quality1080p,
		Source:     core.SourceBluray,
		Codec:      "x264",
		Group:      "GRP",
		Confidence: 0.9,
	}
}

// episodeParse is the parse a well-named episode file yields.
func episodeParse(title string, season int, episodes ...int) core.ParsedRelease {
	return core.ParsedRelease{
		Title:      title,
		Season:     season,
		Episodes:   episodes,
		Quality:    core.Quality1080p,
		Source:     core.SourceWebDL,
		Codec:      "x265",
		Confidence: 0.9,
	}
}
