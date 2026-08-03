package wanted

import (
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

func testProfile() *core.QualityProfile {
	return &core.QualityProfile{
		Name:           "HD",
		Cutoff:         core.Quality1080p,
		Items:          []string{core.Quality2160p, core.Quality1080p, core.Quality720p},
		UpgradeAllowed: true,
	}
}

func release(quality, source string, seeders int) core.Release {
	return core.Release{
		Title:   "Example.Release." + quality,
		Seeders: seeders,
		Parsed:  core.ParsedRelease{Quality: quality, Source: source},
	}
}

func TestScoreReleaseRejectsQualityOutsideProfile(t *testing.T) {
	score, reject := ScoreRelease(release(core.Quality480p, core.SourceWebDL, 10), testProfile())
	if reject == "" {
		t.Fatalf("expected rejection, got score %d", score)
	}
	if score != 0 {
		t.Fatalf("rejected candidate must score 0, got %d", score)
	}
}

func TestScoreReleaseRejectsUnknownQuality(t *testing.T) {
	_, reject := ScoreRelease(release(core.QualityUnknown, core.SourceBluray, 10), testProfile())
	if reject == "" {
		t.Fatal("an unparseable release must not be grabbed on faith")
	}
}

func TestScoreReleasePrefersBetterQuality(t *testing.T) {
	hd, _ := ScoreRelease(release(core.Quality1080p, core.SourceWebDL, 0), testProfile())
	sd, _ := ScoreRelease(release(core.Quality720p, core.SourceWebDL, 0), testProfile())
	if hd <= sd {
		t.Fatalf("1080p (%d) must beat 720p (%d)", hd, sd)
	}
}

func TestScoreReleaseSourceCannotBeatQuality(t *testing.T) {
	webdl1080, _ := ScoreRelease(release(core.Quality1080p, core.SourceCam, 0), testProfile())
	bluray720, _ := ScoreRelease(release(core.Quality720p, core.SourceBluray, seedersCap), testProfile())
	if webdl1080 <= bluray720 {
		t.Fatalf("a worse source on 1080p (%d) must still beat bluray 720p (%d)", webdl1080, bluray720)
	}
}

func TestScoreReleaseProperBreaksTies(t *testing.T) {
	plain, _ := ScoreRelease(release(core.Quality1080p, core.SourceWebDL, 5), testProfile())
	r := release(core.Quality1080p, core.SourceWebDL, 5)
	r.Parsed.Proper = true
	proper, _ := ScoreRelease(r, testProfile())
	if proper <= plain {
		t.Fatalf("PROPER (%d) must beat the same release without it (%d)", proper, plain)
	}
}

func TestBelowCutoff(t *testing.T) {
	p := testProfile()
	if !BelowCutoff(core.Quality720p, p) {
		t.Error("720p is below a 1080p cutoff")
	}
	if BelowCutoff(core.Quality1080p, p) {
		t.Error("1080p meets a 1080p cutoff")
	}
	if BelowCutoff(core.Quality2160p, p) {
		t.Error("2160p exceeds a 1080p cutoff even when not in the items")
	}
	if !BelowCutoff(core.QualityUnknown, p) {
		t.Error("an unknown file quality cannot satisfy a cutoff")
	}
}

func TestIsUpgrade(t *testing.T) {
	if !IsUpgrade(core.Quality1080p, core.Quality720p) {
		t.Error("1080p over 720p is an upgrade")
	}
	if IsUpgrade(core.Quality720p, core.Quality1080p) {
		t.Error("720p over 1080p is a downgrade")
	}
	if IsUpgrade(core.Quality1080p, core.Quality1080p) {
		t.Error("same quality is not an upgrade")
	}
}

func TestSelectBestPicksWinnerAndExplainsLosers(t *testing.T) {
	candidates := []core.Release{
		release(core.Quality720p, core.SourceWebDL, 30),
		release(core.Quality1080p, core.SourceWebDL, 2),
		release(core.Quality480p, core.SourceWebDL, 99),
	}
	best, rejected := SelectBest(candidates, testProfile())
	if best == nil {
		t.Fatal("expected a winner")
	}
	if best.Parsed.Quality != core.Quality1080p {
		t.Fatalf("winner = %q, want 1080p", best.Parsed.Quality)
	}
	if len(rejected) != 2 {
		t.Fatalf("rejected = %d, want 2", len(rejected))
	}
	for _, d := range rejected {
		if d.Reject == "" {
			t.Errorf("rejected %q carries no reason", d.Release.Title)
		}
	}
}

func TestSelectBestNothingAcceptable(t *testing.T) {
	best, rejected := SelectBest([]core.Release{
		release(core.Quality480p, core.SourceHDTV, 10),
	}, testProfile())
	if best != nil {
		t.Fatalf("winner = %q, want nil", best.Title)
	}
	if len(rejected) != 1 || rejected[0].Reject == "" {
		t.Fatalf("the one candidate must be rejected with a reason, got %+v", rejected)
	}
}

// TestAvailable pins the minimum-availability rules to Radarr's semantics
// (Movie.IsAvailable), plus the one documented deviation: no dates at all
// means available, not never.
func TestAvailable(t *testing.T) {
	today := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	day := func(y, m, d int) time.Time { return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC) }

	tests := []struct {
		name  string
		movie core.Movie
		want  bool
	}{
		{
			name:  "announced is available before any date",
			movie: core.Movie{MinAvailability: core.AvailabilityAnnounced, ReleaseDate: day(2027, 1, 1)},
			want:  true,
		},
		{
			name:  "in cinemas waits for the theatrical date",
			movie: core.Movie{MinAvailability: core.AvailabilityInCinemas, ReleaseDate: day(2026, 9, 1)},
			want:  false,
		},
		{
			name:  "in cinemas on the theatrical day itself",
			movie: core.Movie{MinAvailability: core.AvailabilityInCinemas, ReleaseDate: day(2026, 8, 3)},
			want:  true,
		},
		{
			// Radarr's fall-through: no theatrical date must not read as
			// "already in cinemas".
			name: "in cinemas without a theatrical date degrades to released",
			movie: core.Movie{MinAvailability: core.AvailabilityInCinemas,
				DigitalRelease: day(2026, 9, 1)},
			want: false,
		},
		{
			name: "released takes the earlier home date: digital first",
			movie: core.Movie{MinAvailability: core.AvailabilityReleased,
				DigitalRelease: day(2026, 8, 1), PhysicalRelease: day(2026, 12, 1)},
			want: true,
		},
		{
			name: "released takes the earlier home date: physical first",
			movie: core.Movie{MinAvailability: core.AvailabilityReleased,
				DigitalRelease: day(2026, 12, 1), PhysicalRelease: day(2026, 8, 1)},
			want: true,
		},
		{
			name: "released with both home dates ahead",
			movie: core.Movie{MinAvailability: core.AvailabilityReleased,
				DigitalRelease: day(2026, 9, 1), PhysicalRelease: day(2026, 10, 1)},
			want: false,
		},
		{
			name: "released with no home dates waits out the cinema window",
			movie: core.Movie{MinAvailability: core.AvailabilityReleased,
				ReleaseDate: day(2026, 6, 1)},
			want: false,
		},
		{
			name: "released once the cinema window has passed",
			movie: core.Movie{MinAvailability: core.AvailabilityReleased,
				ReleaseDate: day(2026, 5, 1)},
			want: true,
		},
		{
			// The deviation: an episode with no air date is treated as aired,
			// and a movie the provider has no dates for is treated the same
			// way rather than hidden forever.
			name:  "released with no dates at all is available",
			movie: core.Movie{MinAvailability: core.AvailabilityReleased},
			want:  true,
		},
		{
			name:  "empty availability reads as released",
			movie: core.Movie{ReleaseDate: day(2026, 6, 1)},
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Available(tt.movie, today); got != tt.want {
				t.Errorf("Available = %v, want %v", got, tt.want)
			}
		})
	}
}
