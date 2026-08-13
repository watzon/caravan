package store

import (
	"context"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// Bun Value overrides only apply to columns included in the model update. Pin
// that detail because library_id = 0 must retain the stored library, while a
// non-zero ID explicitly moves the item.
func TestCatalogUpsertsPreserveAndExplicitlyUpdateLibraryID(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	var movieLibraryID, tvLibraryID int64
	if err := st.DB().QueryRowContext(ctx,
		"SELECT id FROM libraries WHERE kind = 'movie' AND is_default = 1").Scan(&movieLibraryID); err != nil {
		t.Fatalf("movie library: %v", err)
	}
	if err := st.DB().QueryRowContext(ctx,
		"SELECT id FROM libraries WHERE kind = 'tv' AND is_default = 1").Scan(&tvLibraryID); err != nil {
		t.Fatalf("tv library: %v", err)
	}

	movie := &core.Movie{TMDBID: 912345, Title: "Movie"}
	if err := st.UpsertMovie(ctx, movie); err != nil {
		t.Fatalf("insert movie: %v", err)
	}
	movieMove := &core.Movie{
		ID: movie.ID, TMDBID: movie.TMDBID, Title: movie.Title, LibraryID: movieLibraryID,
	}
	if err := st.UpsertMovie(ctx, movieMove); err != nil {
		t.Fatalf("move movie: %v", err)
	}
	movieRefresh := &core.Movie{ID: movie.ID, TMDBID: movie.TMDBID, Title: movie.Title}
	if err := st.UpsertMovie(ctx, movieRefresh); err != nil {
		t.Fatalf("refresh movie: %v", err)
	}
	gotMovie, err := st.GetMovie(ctx, movie.ID)
	if err != nil {
		t.Fatalf("get movie: %v", err)
	}
	if gotMovie.LibraryID != movieLibraryID {
		t.Fatalf("movie library = %d, want preserved %d", gotMovie.LibraryID, movieLibraryID)
	}

	series := &core.Series{TMDBID: 923456, Title: "Series"}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("insert series: %v", err)
	}
	seriesMove := &core.Series{
		ID: series.ID, TMDBID: series.TMDBID, Title: series.Title, LibraryID: tvLibraryID,
	}
	if err := st.UpsertSeries(ctx, seriesMove); err != nil {
		t.Fatalf("move series: %v", err)
	}
	seriesRefresh := &core.Series{ID: series.ID, TMDBID: series.TMDBID, Title: series.Title}
	if err := st.UpsertSeries(ctx, seriesRefresh); err != nil {
		t.Fatalf("refresh series: %v", err)
	}
	gotSeries, err := st.GetSeries(ctx, series.ID)
	if err != nil {
		t.Fatalf("get series: %v", err)
	}
	if gotSeries.LibraryID != tvLibraryID {
		t.Fatalf("series library = %d, want preserved %d", gotSeries.LibraryID, tvLibraryID)
	}
}
