package searchql

import (
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

func movieRelease() core.Release {
	return core.Release{
		Indexer: "NZBGeek",
		Title:   "Dune.Part.Two.2024.2160p.UHD.BluRay.x265.10bit.DTS-HD.MA-CRAVE",
		Parsed: core.ParsedRelease{
			Title:    "Dune Part Two",
			Year:     2024,
			Quality:  core.Quality2160p,
			Source:   core.SourceBluray,
			Codec:    "x265",
			Audio:    "DTS-HD",
			BitDepth: 10,
			Group:    "CRAVE",
			Edition:  "Director's Cut",
			Proper:   true,
		},
	}
}

func episodeRelease() core.Release {
	return core.Release{
		Indexer: "Torznab Test",
		Title:   "Some.Show.S03E04.1080p.WEB-DL.H264-GRP",
		Parsed: core.ParsedRelease{
			Title:    "Some Show",
			Season:   3,
			Episodes: []int{4},
			Quality:  core.Quality1080p,
			Source:   core.SourceWebDL,
			Repack:   true,
		},
	}
}

func seasonPackRelease() core.Release {
	return core.Release{
		Title: "Some.Show.S03.1080p.WEB-DL.H264-GRP",
		Parsed: core.ParsedRelease{
			Title: "Some Show", Season: 3, Quality: core.Quality1080p,
		},
	}
}

func sceneRelease() core.Release {
	return core.Release{
		Title: "CreampieThais.26.01.19.Some.Performer.XXX.1080p",
		Parsed: core.ParsedRelease{
			Title:     "CreampieThais",
			Year:      2026,
			Season:    2026,
			SceneDate: time.Date(2026, 1, 19, 0, 0, 0, 0, time.UTC),
			Quality:   core.Quality1080p,
		},
	}
}

// unparsedRelease is what an indexer result looks like when the parser could
// make nothing of the name: the raw title is the only text there is.
func unparsedRelease() core.Release {
	return core.Release{Indexer: "NZBGeek", Title: "Some.Raw.Name.Nobody.Parsed"}
}

func TestMatches(t *testing.T) {
	cases := []struct {
		name  string
		query string
		rel   core.Release
		want  bool
	}{
		// A positive keyword was already answered by the indexer.
		{"a positive keyword passes even off-title", "avatar", movieRelease(), true},
		{"a positive phrase passes even off-title", `"nothing like this"`, movieRelease(), true},
		{"a negated keyword rejects what the name contains", "-bluray", movieRelease(), false},
		{"a negated keyword keeps what the name lacks", "-cam", movieRelease(), true},
		{"a negated phrase reads the whole phrase", `-"part two"`, movieRelease(), false},
		{"a negated phrase the name lacks", `-"part three"`, movieRelease(), true},
		{"negation reads punctuation as a word break", `-"web dl"`, episodeRelease(), false},
		{"punctuation carries no constraint", `-"..."`, movieRelease(), true},
		{"double negation is positive again", "NOT -bluray", movieRelease(), true},

		{"title matches the parsed title", "title:dune", movieRelease(), true},
		{"title is a substring test", `title:"part two"`, movieRelease(), true},
		{"title rejects another work", "title:matrix", movieRelease(), false},
		{"title falls back to the raw name", `title:"some raw"`, unparsedRelease(), true},

		// A site writes itself run-together in the release name and with spaces
		// in the seed expression, so site: has to reach across the difference.
		{"site matches the run-together name", `site:"creampie thais"`, sceneRelease(), true},
		{"site matches when spelled the way the release is", "site:creampiethais", sceneRelease(), true},
		{"site rejects another site", "site:blacked", sceneRelease(), false},
		{"site reads the raw name when nothing parsed", "site:raw", unparsedRelease(), true},

		{"year matches", "year:2024", movieRelease(), true},
		{"year rejects", "year:2023", movieRelease(), false},
		{"an unknown year matches nothing", "year:2024", episodeRelease(), false},
		{"an unreadable year is false, not an error", "year:soon", movieRelease(), false},

		{"season matches", "season:3", episodeRelease(), true},
		{"season rejects", "season:2", episodeRelease(), false},
		{"episode matches one of the numbers", "episode:4", episodeRelease(), true},
		{"episode rejects", "episode:5", episodeRelease(), false},
		{"a release with no episodes matches no episode", "episode:4", seasonPackRelease(), false},

		{"date matches the scene date", "date:2026-01-19", sceneRelease(), true},
		{"date accepts the release-name spelling", "date:26.01.19", sceneRelease(), true},
		{"date rejects another day", "date:2026-01-20", sceneRelease(), false},
		{"a release with no scene date matches no date", "date:2026-01-19", movieRelease(), false},
		{"an unreadable date is false, not an error", "date:sometime", sceneRelease(), false},

		{"quality matches", "quality:2160p", movieRelease(), true},
		{"quality accepts the 4k alias", "quality:4k", movieRelease(), true},
		{"quality is case insensitive", "quality:UHD", movieRelease(), true},
		{"quality rejects another rung", "quality:1080p", movieRelease(), false},

		{"source matches whatever case", "source:BluRay", movieRelease(), true},
		{"source rejects", "source:webdl", movieRelease(), false},
		{"codec matches", "codec:X265", movieRelease(), true},
		{"audio matches", "audio:dts-hd", movieRelease(), true},
		{"group matches", "group:crave", movieRelease(), true},
		{"group rejects", "group:other", movieRelease(), false},
		{"bitdepth matches", "bitdepth:10", movieRelease(), true},
		{"bitdepth rejects", "bitdepth:8", movieRelease(), false},
		{"an unreadable bitdepth is false", "bitdepth:ten", movieRelease(), false},
		{"edition is a slug test", `edition:"directors cut"`, movieRelease(), true},
		{"edition rejects", "edition:extended", movieRelease(), false},
		{"an unset field matches nothing", "codec:x265", episodeRelease(), false},

		{"indexer is a contains test", "indexer:geek", movieRelease(), true},
		{"indexer is case insensitive", "indexer:NZBGEEK", movieRelease(), true},
		{"indexer rejects", "indexer:animetosho", movieRelease(), false},

		{"is proper", "is:proper", movieRelease(), true},
		{"is proper rejects", "is:proper", episodeRelease(), false},
		{"is repack", "is:repack", episodeRelease(), true},
		{"a pack names no episode", "is:seasonpack", seasonPackRelease(), true},
		{"an episode is not a pack", "is:seasonpack", episodeRelease(), false},
		{"an unknown is value matches nothing", "is:hdr", movieRelease(), false},

		{"AND needs both", "title:dune year:2024", movieRelease(), true},
		{"AND rejects on either", "title:dune year:2023", movieRelease(), false},
		{"OR needs one", "title:matrix OR title:dune", movieRelease(), true},
		{"OR rejects when neither holds", "title:matrix OR title:blade", movieRelease(), false},
		{"parentheses group", "(title:matrix OR title:dune) year:2024", movieRelease(), true},
		{"NOT inverts a field term", "NOT title:matrix", movieRelease(), true},
		{"NOT of a group is not of the whole group", "NOT (title:matrix year:2024)", movieRelease(), true},
		{"a filter narrows a text search", "dune quality:1080p", movieRelease(), false},
		{"the whole seed expression matches its own release", `title:"Dune Part Two" year:2024`, movieRelease(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustParse(t, tc.query).Matches(tc.rel); got != tc.want {
				t.Fatalf("Matches(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}
