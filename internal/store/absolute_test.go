package store

import (
	"context"
	"errors"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

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

	// A later writer with no opinion. The shape a placeholder row is written
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
