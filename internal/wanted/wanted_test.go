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

func TestScoreReleaseWithContributionsPreservesTotal(t *testing.T) {
	r := release(core.Quality1080p, core.SourceWebDL, 75)
	r.Parsed.Proper = true
	r.Parsed.Repack = true

	score, reject, contributions := ScoreReleaseWithContributions(r, testProfile())
	if reject != "" {
		t.Fatalf("ScoreReleaseWithContributions rejected an accepted release: %q", reject)
	}
	if score != 2_000+500+40+20+50 {
		t.Fatalf("score = %d, want 2610", score)
	}
	if contributions != (ScoreContributions{
		Quality: 2_000,
		Source:  500,
		Proper:  properBonus,
		Repack:  repackBonus,
		Seeders: seedersCap,
	}) {
		t.Fatalf("contributions = %+v", contributions)
	}
	if contributions.Total() != score {
		t.Fatalf("contributions total = %d, want score %d", contributions.Total(), score)
	}

	legacyScore, legacyReject := ScoreRelease(r, testProfile())
	if legacyScore != score || legacyReject != reject {
		t.Fatalf("ScoreRelease = (%d, %q), want (%d, %q)", legacyScore, legacyReject, score, reject)
	}
}

func TestScoreReleaseAppliesAcquisitionPolicy(t *testing.T) {
	t.Run("preferred source order", func(t *testing.T) {
		p := testProfile()
		p.PreferredSources = []string{core.SourceCam, core.SourceBluray}
		cam, reject := ScoreRelease(release(core.Quality1080p, core.SourceCam, 0), p)
		if reject != "" {
			t.Fatalf("CAM rejected: %q", reject)
		}
		bluray, reject := ScoreRelease(release(core.Quality1080p, core.SourceBluray, 0), p)
		if reject != "" || cam <= bluray {
			t.Fatalf("source scores = CAM %d, BluRay %d, reject %q; want configured order", cam, bluray, reject)
		}
	})
	t.Run("neutral proper repack", func(t *testing.T) {
		p := testProfile()
		p.ProperRepackPreference = core.ProperRepackPreferenceNeutral
		plain := release(core.Quality1080p, core.SourceWebDL, 0)
		tagged := plain
		tagged.Parsed.Proper = true
		tagged.Parsed.Repack = true
		plainScore, _ := ScoreRelease(plain, p)
		taggedScore, _ := ScoreRelease(tagged, p)
		if taggedScore != plainScore {
			t.Fatalf("neutral tagged score = %d, plain score = %d", taggedScore, plainScore)
		}
	})
	t.Run("torrent seeders do not reject usenet", func(t *testing.T) {
		p := testProfile()
		p.MinSeeders = 10
		torrent := release(core.Quality1080p, core.SourceWebDL, 9)
		torrent.Protocol = core.ProtocolTorrent
		if _, reject := ScoreRelease(torrent, p); reject == "" {
			t.Fatal("torrent below the seeder minimum was accepted")
		}
		usenet := torrent
		usenet.Protocol = core.ProtocolUsenet
		if _, reject := ScoreRelease(usenet, p); reject != "" {
			t.Fatalf("usenet release rejected by torrent seeder minimum: %q", reject)
		}
	})
	t.Run("known size bounds accept unknown size", func(t *testing.T) {
		p := testProfile()
		p.MinSizeMB = 2
		p.MaxSizeMB = 4
		small := release(core.Quality1080p, core.SourceWebDL, 0)
		small.Size = 1 * bytesPerMB
		if _, reject := ScoreRelease(small, p); reject == "" {
			t.Fatal("known small release was accepted")
		}
		large := small
		large.Size = 4*bytesPerMB + 1
		if _, reject := ScoreRelease(large, p); reject == "" {
			t.Fatal("known large release was accepted")
		}
		unknown := small
		unknown.Size = 0
		if _, reject := ScoreRelease(unknown, p); reject != "" {
			t.Fatalf("unknown release size rejected: %q", reject)
		}
	})
	t.Run("custom formats sum and respect exclusions", func(t *testing.T) {
		p := testProfile()
		p.CustomFormats = []core.CustomFormat{
			{Name: "HDR", IncludeTerms: []string{"hdr"}, Score: 25},
			{Name: "BluRay", IncludeTerms: []string{"bluray"}, ExcludeTerms: []string{"remux"}, Score: -10},
		}
		r := release(core.Quality1080p, core.SourceWebDL, 0)
		r.Title = "Example.1080p.HDR.BluRay"
		_, reject, contributions := ScoreReleaseWithContributions(r, p)
		if reject != "" || contributions.CustomFormats != 15 {
			t.Fatalf("custom format contribution = %+v, reject %q; want 15", contributions, reject)
		}
		r.Title += ".REMUX"
		_, reject, contributions = ScoreReleaseWithContributions(r, p)
		if reject != "" || contributions.CustomFormats != 25 {
			t.Fatalf("excluded custom format contribution = %+v, reject %q; want 25", contributions, reject)
		}
	})
}

func TestScoreReleaseBoundsCustomFormatScores(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	tests := []struct {
		name   string
		scores []int
		want   int
	}{
		{
			name:   "caps a positive individual score",
			scores: []int{maxInt},
			want:   MaxCustomFormatScore,
		},
		{
			name:   "caps a negative individual score",
			scores: []int{minInt},
			want:   -MaxCustomFormatScore,
		},
		{
			name:   "does not overflow a positive aggregate",
			scores: []int{maxInt, maxInt},
			want:   MaxCustomFormatScore,
		},
		{
			name:   "does not overflow a negative aggregate",
			scores: []int{minInt, minInt},
			want:   -MaxCustomFormatScore,
		},
		{
			name:   "caps a positive aggregate",
			scores: []int{MaxCustomFormatScore, MaxCustomFormatScore},
			want:   MaxCustomFormatScore,
		},
		{
			name:   "caps a negative aggregate",
			scores: []int{-MaxCustomFormatScore, -MaxCustomFormatScore},
			want:   -MaxCustomFormatScore,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := testProfile()
			for _, score := range tt.scores {
				p.CustomFormats = append(p.CustomFormats, core.CustomFormat{
					IncludeTerms: []string{"match"},
					Score:        score,
				})
			}
			r := release(core.Quality1080p, core.SourceWebDL, 0)
			r.Title = "Example.Release.1080p.match"

			_, reject, contributions := ScoreReleaseWithContributions(r, p)
			if reject != "" {
				t.Fatalf("matching release was rejected: %q", reject)
			}
			if contributions.CustomFormats != tt.want {
				t.Fatalf("custom format score = %d, want %d", contributions.CustomFormats, tt.want)
			}
		})
	}
}

func TestScoreReleaseAppliesTVCompatibilityPolicy(t *testing.T) {
	p := testProfile()
	p.TVProfile = core.TVProfileSafe
	r := release(core.Quality1080p, core.SourceWebDL, 0)
	r.Title = "Example.1080p.x265.10bit.DTS.mkv"
	r.Parsed.Codec = "x265"
	r.Parsed.BitDepth = 10
	r.Parsed.Audio = "DTS"

	p.TVCompatibilityPolicy = core.TVCompatibilityPolicyIgnore
	ignored, reject, contributions := ScoreReleaseWithContributions(r, p)
	if reject != "" || contributions.TVCompatibility != 0 {
		t.Fatalf("ignored compatibility = %d, %+v, %q", ignored, contributions, reject)
	}
	p.TVCompatibilityPolicy = core.TVCompatibilityPolicyPrefer
	preferred, reject, contributions := ScoreReleaseWithContributions(r, p)
	if reject != "" || contributions.TVCompatibility != -60 || preferred != ignored-60 {
		t.Fatalf("preferred compatibility = %d, %+v, %q", preferred, contributions, reject)
	}
	p.TVCompatibilityPolicy = core.TVCompatibilityPolicyRequire
	if _, reject := ScoreRelease(r, p); reject != `release is incompatible for required playback target "safe"` {
		t.Fatalf("required compatibility rejection = %q", reject)
	}

	r = release(core.Quality1080p, core.SourceWebDL, 0)
	p.TVCompatibilityPolicy = core.TVCompatibilityPolicyRequire
	if _, reject := ScoreRelease(r, p); reject != "" {
		t.Fatalf("unknown TV tags were rejected: %q", reject)
	}
}

func TestScoreReleaseWithContributionsRejectsWithoutScore(t *testing.T) {
	score, reject, contributions := ScoreReleaseWithContributions(
		release(core.Quality480p, core.SourceWebDL, 10),
		testProfile(),
	)
	if score != 0 || contributions != (ScoreContributions{}) {
		t.Fatalf("rejected score and contributions = %d, %+v; want zero", score, contributions)
	}
	if want := `quality 480p is not in profile "HD"`; reject != want {
		t.Fatalf("rejection = %q, want %q", reject, want)
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

func TestSelectBestReturnsSoleAcceptedNegativeScore(t *testing.T) {
	p := testProfile()
	p.CustomFormats = []core.CustomFormat{{
		Name:         "Unwanted",
		IncludeTerms: []string{"unwanted"},
		Score:        -10_000,
	}}
	candidate := release(core.Quality1080p, core.SourceWebDL, 0)
	candidate.Title = "Example.Release.1080p.unwanted"

	score, reject := ScoreRelease(candidate, p)
	if reject != "" {
		t.Fatalf("accepted candidate was rejected: %q", reject)
	}
	if score >= -1 {
		t.Fatalf("candidate score = %d, want less than -1", score)
	}

	best, rejected := SelectBest([]core.Release{candidate}, p)
	if best == nil {
		t.Fatal("sole accepted candidate was not selected")
	}
	if best.Title != candidate.Title {
		t.Fatalf("winner = %q, want %q", best.Title, candidate.Title)
	}
	if len(rejected) != 0 {
		t.Fatalf("rejected = %+v, want none", rejected)
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
