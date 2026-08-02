package dlna

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// testURLs is the origin every expected URL in these tests is built on.
var testURLs = urls{origin: "http://caravan.lan:8677"}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestService builds a service over a real store in a temp directory, with
// root pointing at an empty temp dir. Nothing here touches the network:
// advertising only starts at Start, which these tests never call.
func newTestService(t *testing.T) (*Service, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "caravan.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	root := filepath.Join(dir, "media")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("create storage root: %v", err)
	}
	svc := New(st, func(context.Context) (string, error) { return root, nil }, 8677, quietLogger())
	return svc, st, root
}

// seedLibrary writes the fixture every hierarchy test browses: one movie with
// one file, and one series with two seasons where only season 1 has files —
// including a double-episode file, which is the case the object-id scheme has
// to keep distinct.
func seedLibrary(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()

	movie := &core.Movie{
		TMDBID: 10378, Title: "Big Buck Bunny", SortTitle: "big buck bunny", Year: 2008,
		Path: "Movies/Big Buck Bunny (2008)", PosterPath: "Movies/Big Buck Bunny (2008)/poster.jpg",
	}
	if err := st.UpsertMovie(ctx, movie); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	movieFile := &core.MediaFile{
		Path:    "Movies/Big Buck Bunny (2008)/Big Buck Bunny (2008).mkv",
		Size:    1 << 20,
		MovieID: movie.ID,
	}
	if err := st.UpsertMediaFile(ctx, movieFile); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}

	series := &core.Series{
		TMDBID: 68507, Title: "Planet Earth II", SortTitle: "planet earth ii", Year: 2016,
		Path: "TV/Planet Earth II (2016)", PosterPath: "TV/Planet Earth II (2016)/poster.jpg",
	}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	for _, number := range []int{1, 2} {
		if err := st.UpsertSeason(ctx, &core.Season{SeriesID: series.ID, Number: number}); err != nil {
			t.Fatalf("UpsertSeason: %v", err)
		}
	}

	episodes := []*core.Episode{
		{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "Islands"},
		{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 2, Title: "Mountains"},
		{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 3, Title: "Jungles"},
		{SeriesID: series.ID, SeasonNumber: 2, EpisodeNumber: 1, Title: "Unaired"},
	}
	for _, e := range episodes {
		if err := st.UpsertEpisode(ctx, e); err != nil {
			t.Fatalf("UpsertEpisode: %v", err)
		}
	}

	// One file covering E01 and E02, one file covering E03. Season 2 has none.
	double := &core.MediaFile{Path: "TV/Planet Earth II (2016)/Season 01/Planet Earth II (2016) - S01E01E02.mkv", Size: 42}
	if err := st.UpsertMediaFile(ctx, double); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	single := &core.MediaFile{Path: "TV/Planet Earth II (2016)/Season 01/Planet Earth II (2016) - S01E03 - Jungles.mp4", Size: 7}
	if err := st.UpsertMediaFile(ctx, single); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	for _, link := range []struct{ episode, file int64 }{
		{episodes[0].ID, double.ID},
		{episodes[1].ID, double.ID},
		{episodes[2].ID, single.ID},
	} {
		if err := st.LinkEpisodeFile(ctx, link.episode, link.file); err != nil {
			t.Fatalf("LinkEpisodeFile: %v", err)
		}
	}
}

func containerIDs(d *didlLite) []string {
	out := []string{}
	for _, c := range d.Containers {
		out = append(out, c.ID)
	}
	return out
}

func itemTitles(d *didlLite) []string {
	out := []string{}
	for _, i := range d.Items {
		out = append(out, i.Title)
	}
	return out
}

func TestBrowseRootListsMoviesAndTV(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)

	got, err := svc.children(context.Background(), testURLs, rootID)
	if err != nil {
		t.Fatalf("children(root): %v", err)
	}
	if len(got.Items) != 0 {
		t.Fatalf("root has playable items: %+v", got.Items)
	}
	if ids := containerIDs(got); len(ids) != 2 || ids[0] != moviesID || ids[1] != tvID {
		t.Fatalf("root children = %v, want [movies tv]", ids)
	}
	for _, c := range got.Containers {
		if c.ParentID != rootID {
			t.Fatalf("%s parentID = %q, want %q", c.ID, c.ParentID, rootID)
		}
		if c.Class != classContainer {
			t.Fatalf("%s class = %q", c.ID, c.Class)
		}
		if c.Restricted != 1 {
			t.Fatalf("%s is not restricted", c.ID)
		}
	}
	// Movies counts files, TV counts shows.
	if got.Containers[0].ChildCount != 1 {
		t.Fatalf("Movies childCount = %d, want 1", got.Containers[0].ChildCount)
	}
	if got.Containers[1].ChildCount != 1 {
		t.Fatalf("TV childCount = %d, want 1", got.Containers[1].ChildCount)
	}
}

func TestBrowseMoviesCarriesResAndArt(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)

	got, err := svc.children(context.Background(), testURLs, moviesID)
	if err != nil {
		t.Fatalf("children(movies): %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("movies = %+v, want one item", got.Items)
	}
	item := got.Items[0]
	if item.Title != "Big Buck Bunny (2008)" {
		t.Fatalf("title = %q", item.Title)
	}
	if item.ParentID != moviesID {
		t.Fatalf("parentID = %q", item.ParentID)
	}
	if item.Class != classVideoItem {
		t.Fatalf("class = %q", item.Class)
	}
	// The res URL is what the TV actually fetches; it must be absolute, on the
	// origin the client reached us at, and carry the container extension.
	wantRes := "http://caravan.lan:8677/dlna/media/1.mkv"
	if item.Res.URL != wantRes {
		t.Fatalf("res = %q, want %q", item.Res.URL, wantRes)
	}
	if item.Res.Size != 1<<20 {
		t.Fatalf("res size = %d", item.Res.Size)
	}
	if item.Res.ProtocolInfo != protocolInfo("x.mkv") {
		t.Fatalf("protocolInfo = %q", item.Res.ProtocolInfo)
	}
	// Artwork reuses the API's own image endpoint rather than a second copy of
	// the path-confinement logic.
	wantArt := "http://caravan.lan:8677/api/v1/images/Movies/Big%20Buck%20Bunny%20%282008%29/poster.jpg"
	if item.AlbumArtURI != wantArt {
		t.Fatalf("albumArtURI = %q, want %q", item.AlbumArtURI, wantArt)
	}
}

// A movie with no file is not browsable: an entry a remote control can select
// but not play is a dead end.
func TestBrowseMoviesSkipsFilelessMovies(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	if err := st.UpsertMovie(context.Background(), &core.Movie{
		TMDBID: 999, Title: "Wanted But Unowned", SortTitle: "a wanted", Year: 2030,
	}); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	got, err := svc.children(context.Background(), testURLs, moviesID)
	if err != nil {
		t.Fatalf("children(movies): %v", err)
	}
	if titles := itemTitles(got); len(titles) != 1 || titles[0] != "Big Buck Bunny (2008)" {
		t.Fatalf("movies = %v, want only the owned one", titles)
	}
}

func TestBrowseTVDownToEpisodes(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	ctx := context.Background()

	tv, err := svc.children(ctx, testURLs, tvID)
	if err != nil {
		t.Fatalf("children(tv): %v", err)
	}
	if len(tv.Containers) != 1 {
		t.Fatalf("tv = %+v, want one series", tv.Containers)
	}
	seriesObject := tv.Containers[0]
	if seriesObject.Title != "Planet Earth II (2016)" {
		t.Fatalf("series title = %q", seriesObject.Title)
	}
	if seriesObject.ParentID != tvID {
		t.Fatalf("series parentID = %q", seriesObject.ParentID)
	}
	if seriesObject.ChildCount != 2 {
		t.Fatalf("series childCount = %d, want 2 seasons", seriesObject.ChildCount)
	}

	seasons, err := svc.children(ctx, testURLs, seriesObject.ID)
	if err != nil {
		t.Fatalf("children(series): %v", err)
	}
	if len(seasons.Containers) != 2 {
		t.Fatalf("seasons = %+v", seasons.Containers)
	}
	// childCount counts playable files, not episode rows: season 1 holds three
	// episodes across two files, and the double file counts once per episode.
	if seasons.Containers[0].Title != "Season 1" || seasons.Containers[0].ChildCount != 3 {
		t.Fatalf("season 1 = %+v, want 3 playable children", seasons.Containers[0])
	}
	// Season 2 has an episode row but no file, so it says so instead of
	// promising something to open.
	if seasons.Containers[1].Title != "Season 2" || seasons.Containers[1].ChildCount != 0 {
		t.Fatalf("season 2 = %+v, want 0 playable children", seasons.Containers[1])
	}
	if seasons.Containers[0].ParentID != seriesObject.ID {
		t.Fatalf("season parentID = %q, want %q", seasons.Containers[0].ParentID, seriesObject.ID)
	}

	episodes, err := svc.children(ctx, testURLs, seasons.Containers[0].ID)
	if err != nil {
		t.Fatalf("children(season 1): %v", err)
	}
	want := []string{"S01E01 - Islands", "S01E02 - Mountains", "S01E03 - Jungles"}
	got := itemTitles(episodes)
	if len(got) != len(want) {
		t.Fatalf("season 1 items = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d = %q, want %q", i, got[i], want[i])
		}
	}

	// The double file is one file behind two objects: same res URL, distinct
	// object ids, so a client's per-item bookmarks do not collide.
	if episodes.Items[0].Res.URL != episodes.Items[1].Res.URL {
		t.Fatalf("double-episode file has two res URLs: %q vs %q",
			episodes.Items[0].Res.URL, episodes.Items[1].Res.URL)
	}
	if episodes.Items[0].ID == episodes.Items[1].ID {
		t.Fatalf("double-episode file collapsed into one object id %q", episodes.Items[0].ID)
	}
	if episodes.Items[2].Res.URL != "http://caravan.lan:8677/dlna/media/3.mp4" {
		t.Fatalf("S01E03 res = %q", episodes.Items[2].Res.URL)
	}

	// An empty season answers with an empty document, not an error.
	empty, err := svc.children(ctx, testURLs, seasons.Containers[1].ID)
	if err != nil {
		t.Fatalf("children(season 2): %v", err)
	}
	if empty.count() != 0 {
		t.Fatalf("season 2 = %+v, want empty", empty)
	}
}

func TestBrowseMetadata(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	ctx := context.Background()

	// The root reports itself under the device's friendly name and claims the
	// parent every client expects.
	root, err := svc.metadata(ctx, testURLs, rootID)
	if err != nil {
		t.Fatalf("metadata(root): %v", err)
	}
	if len(root.Containers) != 1 {
		t.Fatalf("root metadata = %+v", root)
	}
	if root.Containers[0].ParentID != rootParentID {
		t.Fatalf("root parentID = %q, want %q", root.Containers[0].ParentID, rootParentID)
	}
	if root.Containers[0].Title != DefaultFriendlyName {
		t.Fatalf("root title = %q", root.Containers[0].Title)
	}
	if root.Containers[0].ChildCount != 2 {
		t.Fatalf("root childCount = %d", root.Containers[0].ChildCount)
	}

	// An item's metadata has to reproduce exactly what the browse handed out,
	// because that is how a client resolves an id it saved.
	children, err := svc.children(ctx, testURLs, moviesID)
	if err != nil {
		t.Fatalf("children(movies): %v", err)
	}
	one, err := svc.metadata(ctx, testURLs, children.Items[0].ID)
	if err != nil {
		t.Fatalf("metadata(movie item): %v", err)
	}
	if len(one.Items) != 1 || one.Items[0] != children.Items[0] {
		t.Fatalf("metadata = %+v, want %+v", one.Items, children.Items[0])
	}

	seasons, err := svc.children(ctx, testURLs, "s:1")
	if err != nil {
		t.Fatalf("children(series): %v", err)
	}
	episodes, err := svc.children(ctx, testURLs, seasons.Containers[0].ID)
	if err != nil {
		t.Fatalf("children(season): %v", err)
	}
	ep, err := svc.metadata(ctx, testURLs, episodes.Items[0].ID)
	if err != nil {
		t.Fatalf("metadata(episode item): %v", err)
	}
	if len(ep.Items) != 1 || ep.Items[0] != episodes.Items[0] {
		t.Fatalf("metadata = %+v, want %+v", ep.Items, episodes.Items[0])
	}

	season, err := svc.metadata(ctx, testURLs, seasons.Containers[0].ID)
	if err != nil {
		t.Fatalf("metadata(season): %v", err)
	}
	if len(season.Containers) != 1 || season.Containers[0].ParentID != "s:1" {
		t.Fatalf("season metadata = %+v", season.Containers)
	}
}

// A client that cached an object id across a rescan must get "no such object",
// which the SOAP layer renders as error 701, rather than a server error.
func TestBrowseUnknownObject(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	ctx := context.Background()

	for _, id := range []string{"nope", "m:999", "s:999", "s:1:99:1", "e:999:1", "m:notanumber", "e:1"} {
		if _, err := svc.metadata(ctx, testURLs, id); !errors.Is(err, errNoObject) {
			t.Fatalf("metadata(%q) err = %v, want errNoObject", id, err)
		}
	}
	// Browsing the children of a bad id fails the same way, except for the
	// well-formed-but-absent series, which fails on the lookup.
	for _, id := range []string{"nope", "s:999", "s:1:99:1"} {
		if _, err := svc.children(ctx, testURLs, id); !errors.Is(err, errNoObject) {
			t.Fatalf("children(%q) err = %v, want errNoObject", id, err)
		}
	}

	// A season number nobody has still browses to nothing rather than failing:
	// the series exists, the season is simply empty.
	empty, err := svc.children(ctx, testURLs, "s:1:99")
	if err != nil {
		t.Fatalf("children(unknown season): %v", err)
	}
	if empty.count() != 0 {
		t.Fatalf("unknown season = %+v, want empty", empty)
	}
}

// An empty library is a valid library: a first-run Caravan is browsable and
// simply has nothing in it.
func TestBrowseEmptyLibrary(t *testing.T) {
	svc, _, _ := newTestService(t)

	got, err := svc.children(context.Background(), testURLs, rootID)
	if err != nil {
		t.Fatalf("children(root): %v", err)
	}
	if len(got.Containers) != 2 {
		t.Fatalf("root = %+v", got.Containers)
	}
	for _, c := range got.Containers {
		if c.ChildCount != 0 {
			t.Fatalf("%s childCount = %d, want 0", c.ID, c.ChildCount)
		}
	}
}
