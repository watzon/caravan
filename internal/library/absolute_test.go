package library

import (
	"context"
	"path/filepath"
	"testing"
)

// The provider's absolute numbers have to survive the rest of the import, and
// the rest of the import writes episode rows too: a file whose episode the
// provider never listed creates a placeholder row, and a rescan walks the whole
// library again. Neither may cost the series its numbering — a number that
// vanished on the second scan is worse than one that never arrived, because
// everything downstream would have been built on it in between.
func TestScanKeepsProviderAbsoluteNumbers(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	meta := seedSeries(h)
	for i := range meta.Seasons[0].Episodes {
		meta.Seasons[0].Episodes[i].Absolute = i + 1
	}
	h.provider.seriesByID[42] = meta

	// Episode 4 is on disk and in no provider tree, which is what makes
	// ensureEpisodes write a row of its own.
	raw := "library/TV/Planet.Earth.II.S01E04.1080p.WEB-DL.x265.mkv"
	h.parser[filepath.Base(raw)] = episodeParse("Planet Earth II", 1, 4)
	h.writeVideo(raw, "episode bytes")

	absolutes := func(when string) map[int]int {
		t.Helper()
		series, err := h.st.ListSeries(ctx)
		if err != nil || len(series) != 1 {
			t.Fatalf("ListSeries %s: %v (%d rows)", when, err, len(series))
		}
		episodes, err := h.st.ListEpisodes(ctx, series[0].ID)
		if err != nil {
			t.Fatalf("ListEpisodes %s: %v", when, err)
		}
		got := map[int]int{}
		for _, e := range episodes {
			got[e.EpisodeNumber] = e.AbsoluteNumber
		}
		return got
	}

	if res := h.scan(); res.Added != 1 || res.Unmatched != 0 {
		t.Fatalf("first scan = %+v, want the file imported", res)
	}
	// 1-3 from the tree; 4 is the placeholder, and 0 is the honest answer for
	// an episode no provider has numbered.
	want := map[int]int{1: 1, 2: 2, 3: 3, 4: 0}
	for number, absolute := range want {
		if got := absolutes("after the first scan")[number]; got != absolute {
			t.Errorf("episode %d absolute = %d after the first scan, want %d", number, got, absolute)
		}
	}

	// The second scan re-imports the organized file, re-writes the tree, and
	// meets the placeholder row again.
	h.scan()
	for number, absolute := range want {
		if got := absolutes("after the second scan")[number]; got != absolute {
			t.Errorf("episode %d absolute = %d after the second scan, want %d — a rescan may not renumber",
				number, got, absolute)
		}
	}
}

// A provider that serves no absolute numbers leaves every row at 0 rather than
// at a count taken here. Counting would be an answer Caravan invented about a
// series it does not publish, and it is wrong the moment a special or a split
// cour enters the order.
func TestScanInventsNoAbsoluteNumbers(t *testing.T) {
	h := newHarness(t)
	seedSeries(h) // its episodes carry no Absolute at all
	raw := "library/TV/Planet.Earth.II.S01E01.1080p.WEB-DL.x265.mkv"
	h.parser[filepath.Base(raw)] = episodeParse("Planet Earth II", 1, 1)
	h.writeVideo(raw, "episode bytes")
	h.scan()

	ctx := context.Background()
	series, err := h.st.ListSeries(ctx)
	if err != nil || len(series) != 1 {
		t.Fatalf("ListSeries: %v (%d rows)", err, len(series))
	}
	episodes, err := h.st.ListEpisodes(ctx, series[0].ID)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	for _, e := range episodes {
		if e.AbsoluteNumber != 0 {
			t.Errorf("S%02dE%02d absolute = %d, want 0", e.SeasonNumber, e.EpisodeNumber, e.AbsoluteNumber)
		}
	}
	if _, err := h.st.GetEpisodeByAbsoluteNumber(ctx, series[0].ID, 1); err == nil {
		t.Error("GetEpisodeByAbsoluteNumber(1) found a row; nothing served that number")
	}
}
