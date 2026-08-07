package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// 0025 adds one integer per episode, and an upgraded install must come out the
// other side saying what it already said: every row it wrote before this
// migration knows no absolute number, and 0 is how the column spells that.
// Nothing is backfilled, because a number counted here would be an answer
// Caravan invented rather than one a provider served.
func TestMigrate0025AddsAbsoluteNumbering(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "caravan.db")
	openAtSchemaVersion(t, path, 24)

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("open seeded database: %v", err)
	}
	exec(t, db, `INSERT INTO series (id, kind, title, sort_title, year, added_at, updated_at)
		VALUES (7, 'tv', 'Frieren', 'frieren', 2023,
		        '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`)
	exec(t, db, `INSERT INTO episodes (id, series_id, season_number, episode_number, title)
		VALUES (70, 7, 1, 1, 'The Journey''s End')`)
	if err := db.Close(); err != nil {
		t.Fatalf("close seeded database: %v", err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open after upgrade: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	version, err := st.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if version < 25 {
		t.Fatalf("schema version = %d, want at least 25", version)
	}

	// The column reads back through the ordinary door, which is the only proof
	// that matters: the scan list and the column list agree.
	e, err := st.GetEpisode(ctx, 70)
	if err != nil {
		t.Fatalf("GetEpisode: %v", err)
	}
	if e.AbsoluteNumber != 0 {
		t.Errorf("pre-existing episode absolute = %d, want 0 — nothing may be invented for it",
			e.AbsoluteNumber)
	}
	// And a zero absolute is not an identity: the lookup refuses it rather than
	// handing back the first row that never had one.
	if _, err := st.GetEpisodeByAbsoluteNumber(ctx, 7, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetEpisodeByAbsoluteNumber(0) error = %v, want ErrNotFound", err)
	}
}

// The absolute number is a provider's fact, written by whichever refresh heard
// it and preserved against every writer that never did. A caller holding an
// episode struct it did not fill from a provider tree writes 0, and 0 must read
// as "I have nothing to say", never as "forget what you knew".
func TestUpsertEpisodePreservesAbsoluteNumber(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	sr := &core.Series{Title: "Frieren"}
	if err := st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}

	// The refresh: the provider's tree, absolute numbers and all.
	known := &core.Episode{SeriesID: sr.ID, SeasonNumber: 5, EpisodeNumber: 3,
		Title: "Bound", AbsoluteNumber: 105}
	if err := st.UpsertEpisode(ctx, known); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	got, err := st.GetEpisode(ctx, known.ID)
	if err != nil {
		t.Fatalf("GetEpisode: %v", err)
	}
	if got.AbsoluteNumber != 105 {
		t.Fatalf("stored absolute = %d, want the provider's 105", got.AbsoluteNumber)
	}

	// A later writer with no opinion — the shape a placeholder row is written
	// in. It changes the title and must leave the number alone.
	silent := &core.Episode{SeriesID: sr.ID, SeasonNumber: 5, EpisodeNumber: 3, Title: "Bound (fixed)"}
	if err := st.UpsertEpisode(ctx, silent); err != nil {
		t.Fatalf("UpsertEpisode (silent): %v", err)
	}
	got, err = st.GetEpisode(ctx, known.ID)
	if err != nil {
		t.Fatalf("GetEpisode after silent upsert: %v", err)
	}
	if got.AbsoluteNumber != 105 {
		t.Errorf("absolute after a zero-valued upsert = %d, want 105 preserved", got.AbsoluteNumber)
	}
	if got.Title != "Bound (fixed)" {
		t.Errorf("title after upsert = %q, want the new one — only the absolute is preserved", got.Title)
	}

	// An upstream renumbering still lands: preserving a zero is not the same as
	// refusing every later number.
	renumbered := &core.Episode{SeriesID: sr.ID, SeasonNumber: 5, EpisodeNumber: 3,
		Title: "Bound", AbsoluteNumber: 106}
	if err := st.UpsertEpisode(ctx, renumbered); err != nil {
		t.Fatalf("UpsertEpisode (renumbered): %v", err)
	}
	got, err = st.GetEpisode(ctx, known.ID)
	if err != nil {
		t.Fatalf("GetEpisode after renumber: %v", err)
	}
	if got.AbsoluteNumber != 106 {
		t.Errorf("absolute after a renumbering = %d, want 106", got.AbsoluteNumber)
	}
}

// GetEpisodeByAbsoluteNumber is the store-level answer to what an anime-style
// filename asks. It answers only about numbers a provider actually served.
func TestGetEpisodeByAbsoluteNumber(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	sr := &core.Series{Title: "Frieren"}
	if err := st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	want := &core.Episode{SeriesID: sr.ID, SeasonNumber: 5, EpisodeNumber: 3, AbsoluteNumber: 105}
	if err := st.UpsertEpisode(ctx, want); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	// A neighbour the provider served no absolute number for. It is the row a
	// zero-matching lookup would wrongly return.
	if err := st.UpsertEpisode(ctx,
		&core.Episode{SeriesID: sr.ID, SeasonNumber: 5, EpisodeNumber: 4}); err != nil {
		t.Fatalf("UpsertEpisode (no absolute): %v", err)
	}

	got, err := st.GetEpisodeByAbsoluteNumber(ctx, sr.ID, 105)
	if err != nil {
		t.Fatalf("GetEpisodeByAbsoluteNumber(105): %v", err)
	}
	if got.ID != want.ID || got.SeasonNumber != 5 || got.EpisodeNumber != 3 {
		t.Errorf("absolute 105 = %+v, want S05E03 (row %d)", got, want.ID)
	}

	for _, absolute := range []int{0, -1, 999} {
		if _, err := st.GetEpisodeByAbsoluteNumber(ctx, sr.ID, absolute); !errors.Is(err, ErrNotFound) {
			t.Errorf("GetEpisodeByAbsoluteNumber(%d) error = %v, want ErrNotFound", absolute, err)
		}
	}
	// Absolute numbers belong to one series, not to the table.
	other := &core.Series{Title: "Another Show"}
	if err := st.UpsertSeries(ctx, other); err != nil {
		t.Fatalf("UpsertSeries (other): %v", err)
	}
	if _, err := st.GetEpisodeByAbsoluteNumber(ctx, other.ID, 105); !errors.Is(err, ErrNotFound) {
		t.Errorf("another series' absolute 105 error = %v, want ErrNotFound", err)
	}
}
