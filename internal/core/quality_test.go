package core

import "testing"

func TestQualityRank(t *testing.T) {
	tests := []struct {
		quality string
		want    int
	}{
		{quality: Quality2160p, want: 0},
		{quality: Quality1080p, want: 1},
		{quality: Quality720p, want: 2},
		{quality: Quality480p, want: 3},
		{quality: QualityUnknown, want: len(QualityLadder)},
		{quality: "", want: len(QualityLadder)},
		{quality: "8k", want: len(QualityLadder)},
	}

	for _, tt := range tests {
		t.Run(tt.quality, func(t *testing.T) {
			if got := QualityRank(tt.quality); got != tt.want {
				t.Errorf("QualityRank(%q) = %d, want %d", tt.quality, got, tt.want)
			}
		})
	}
}

// The ladder is ordered best-first; scoring code relies on that, so pin it.
func TestQualityLadderIsOrderedBestFirst(t *testing.T) {
	want := []string{Quality2160p, Quality1080p, Quality720p, Quality480p}
	if len(QualityLadder) != len(want) {
		t.Fatalf("QualityLadder = %v, want %v", QualityLadder, want)
	}
	for i, q := range want {
		if QualityLadder[i] != q {
			t.Errorf("QualityLadder[%d] = %q, want %q", i, QualityLadder[i], q)
		}
	}
	for _, q := range QualityLadder {
		if q == QualityUnknown {
			t.Error("QualityLadder must not contain QualityUnknown")
		}
	}
}

func TestSourceRank(t *testing.T) {
	tests := []struct {
		source string
		want   int
	}{
		{source: SourceBluray, want: 0},
		{source: SourceWebDL, want: 1},
		{source: SourceWebRip, want: 2},
		{source: SourceHDTV, want: 3},
		{source: SourceDVD, want: 4},
		{source: SourceCam, want: 5},
		{source: SourceUnknown, want: len(SourceLadder)},
		{source: "telesync", want: len(SourceLadder)},
	}

	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			if got := SourceRank(tt.source); got != tt.want {
				t.Errorf("SourceRank(%q) = %d, want %d", tt.source, got, tt.want)
			}
		})
	}
}

func TestSourceLadderExcludesUnknown(t *testing.T) {
	for _, s := range SourceLadder {
		if s == SourceUnknown {
			t.Error("SourceLadder must not contain SourceUnknown")
		}
	}
}

func TestParsedReleaseIsEpisode(t *testing.T) {
	tests := []struct {
		name   string
		parsed ParsedRelease
		want   bool
	}{
		{name: "movie", parsed: ParsedRelease{Title: "Big Buck Bunny", Year: 2008}, want: false},
		{name: "single episode", parsed: ParsedRelease{Season: 1, Episodes: []int{1}}, want: true},
		{name: "multi episode", parsed: ParsedRelease{Season: 1, Episodes: []int{1, 2}}, want: true},
		{name: "specials", parsed: ParsedRelease{Season: 0, Episodes: []int{3}}, want: true},
		{name: "empty episode slice", parsed: ParsedRelease{Season: 1, Episodes: []int{}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.parsed.IsEpisode(); got != tt.want {
				t.Errorf("IsEpisode() = %v, want %v", got, tt.want)
			}
		})
	}
}
