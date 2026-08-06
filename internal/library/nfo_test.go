package library

import (
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

func TestWriteMovieNFO(t *testing.T) {
	h := newHarness(t)
	meta := &core.MovieMeta{
		TMDBID:        10378,
		IMDBID:        "tt1254207",
		Title:         "Big Buck Bunny",
		OriginalTitle: "Big Buck Bunny",
		Year:          2008,
		Overview:      "A big rabbit & a bad day.",
		ReleaseDate:   time.Date(2008, 5, 20, 0, 0, 0, 0, time.UTC),
	}

	dir := movieDir(stockMovieLib(), meta.Title, meta.Year)
	if err := h.mgr.writeMovieNFO(dir, meta); err != nil {
		t.Fatalf("writeMovieNFO: %v", err)
	}

	want := `<?xml version="1.0" encoding="UTF-8"?>
<movie>
  <title>Big Buck Bunny</title>
  <originaltitle>Big Buck Bunny</originaltitle>
  <sorttitle>big buck bunny</sorttitle>
  <year>2008</year>
  <plot>A big rabbit &amp; a bad day.</plot>
  <premiered>2008-05-20</premiered>
  <uniqueid type="tmdb" default="true">10378</uniqueid>
  <uniqueid type="imdb">tt1254207</uniqueid>
</movie>
`
	if got := h.read(dir + "/" + MovieNFOName); got != want {
		t.Errorf("movie.nfo =\n%s\nwant\n%s", got, want)
	}
}

func TestWriteTVShowNFO(t *testing.T) {
	h := newHarness(t)
	meta := &core.SeriesMeta{
		TMDBID:       42,
		TVDBID:       137,
		Title:        "The Expanse",
		Year:         2015,
		Overview:     "Belters.",
		Status:       "Ended",
		FirstAirDate: time.Date(2015, 12, 14, 0, 0, 0, 0, time.UTC),
	}

	dir := seriesDir(stockTVLib(), meta.Title, meta.Year)
	if err := h.mgr.writeTVShowNFO(dir, meta); err != nil {
		t.Fatalf("writeTVShowNFO: %v", err)
	}

	want := `<?xml version="1.0" encoding="UTF-8"?>
<tvshow>
  <title>The Expanse</title>
  <sorttitle>expanse</sorttitle>
  <year>2015</year>
  <plot>Belters.</plot>
  <premiered>2015-12-14</premiered>
  <status>Ended</status>
  <uniqueid type="tmdb" default="true">42</uniqueid>
  <uniqueid type="tvdb">137</uniqueid>
</tvshow>
`
	if got := h.read(dir + "/" + TVShowNFOName); got != want {
		t.Errorf("tvshow.nfo =\n%s\nwant\n%s", got, want)
	}
}

// TestEnsurePosterSkipsWhenThereIsNothingToFetch covers the two silent no-ops:
// a provider with no image, and a poster that is already on disk.
func TestEnsurePosterSkipsWhenThereIsNothingToFetch(t *testing.T) {
	h := newHarness(t)
	dir := movieDir(stockMovieLib(), "Movie", 2000)

	rel, err := h.mgr.ensurePoster(t.Context(), dir, "")
	if err != nil {
		t.Fatalf("ensurePoster with no URL: %v", err)
	}
	if rel != "" {
		t.Errorf("ensurePoster with no URL returned %q, want empty", rel)
	}
	if h.posterHits != 0 {
		t.Errorf("ensurePoster fetched %d times with no URL", h.posterHits)
	}

	rel, err = h.mgr.ensurePoster(t.Context(), dir, h.posterURL)
	if err != nil {
		t.Fatalf("ensurePoster: %v", err)
	}
	if want := dir + "/" + PosterName; rel != want {
		t.Fatalf("ensurePoster returned %q, want %q", rel, want)
	}
	if h.posterHits != 1 {
		t.Fatalf("poster fetched %d times, want 1", h.posterHits)
	}

	if _, err := h.mgr.ensurePoster(t.Context(), dir, h.posterURL); err != nil {
		t.Fatalf("ensurePoster (second call): %v", err)
	}
	if h.posterHits != 1 {
		t.Errorf("poster refetched though it was already on disk: %d hits", h.posterHits)
	}
}

// TestScanSurvivesAPosterFailure: a dead image host degrades to a warning, it
// does not stop the file being imported (SPEC §13).
func TestScanSurvivesAPosterFailure(t *testing.T) {
	h := newHarness(t)
	seedMovie(h)
	h.provider.movies[0].PosterURL = "http://127.0.0.1:1/poster.jpg"
	h.provider.movieByID[10378] = h.provider.movies[0]
	h.writeVideo(rawMovieRel, "movie bytes")

	res := h.scan()
	if res.Added != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("errors = %v, want exactly one poster warning", res.Errors)
	}
	if !h.exists(organizedRel) {
		t.Errorf("file was not organized despite the poster failure")
	}
	if h.exists(movieDirRel + "/" + PosterName) {
		t.Errorf("a poster was written from a failed download")
	}
}
