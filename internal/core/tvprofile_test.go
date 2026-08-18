package core

import (
	"strings"
	"testing"
)

func TestResolveTVProfile(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{id: "", want: TVProfileSafe},
		{id: TVProfileSafe, want: TVProfileSafe},
		{id: TVProfileCapable, want: TVProfileCapable},
		// A profile a later build removed must fall back, never break the
		// picker.
		{id: "does-not-exist", want: TVProfileSafe},
	}
	for _, tt := range tests {
		if got := ResolveTVProfile(tt.id).ID; got != tt.want {
			t.Errorf("ResolveTVProfile(%q).ID = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestTVProfilesAreIndependentCopies(t *testing.T) {
	first := TVProfiles()
	if len(first) < 2 {
		t.Fatalf("TVProfiles() returned %d profiles, want at least 2", len(first))
	}
	first[0].ID = "mutated"
	if got := TVProfiles()[0].ID; got != TVProfileSafe {
		t.Errorf("TVProfiles()[0].ID = %q after mutating a copy, want %q", got, TVProfileSafe)
	}
}

func TestTVProfileCheck(t *testing.T) {
	tests := []struct {
		name        string
		profile     string
		tags        MediaTags
		wantVerdict string
		// wantReason is a substring every case asserts against the joined
		// reasons: the wording is what the user reads, so it is pinned.
		wantReason string
		// wantNoReason must not appear; it is what catches a verdict that is
		// right for the wrong reason.
		wantNoReason string
	}{
		{
			name:        "nothing tagged is unknown, not a complaint",
			profile:     TVProfileSafe,
			tags:        MediaTags{},
			wantVerdict: TVCompatUnknown,
		},
		{
			name:        "resolution alone cannot clear a release",
			profile:     TVProfileSafe,
			tags:        MediaTags{Quality: Quality1080p},
			wantVerdict: TVCompatUnknown,
		},
		{
			name:        "unknown quality is not a resolution complaint",
			profile:     TVProfileSafe,
			tags:        MediaTags{Codec: "x264", Audio: "AAC", Container: "mp4", Quality: QualityUnknown},
			wantVerdict: TVCompatCompatible,
		},
		{
			name:        "an unrecognized codec tag is not judged",
			profile:     TVProfileSafe,
			tags:        MediaTags{Codec: "someneWcodec"},
			wantVerdict: TVCompatUnknown,
		},
		{
			name:        "safe default clears H.264 AAC MP4 1080p",
			profile:     TVProfileSafe,
			tags:        MediaTags{Codec: "x264", BitDepth: 8, Audio: "AAC", Container: "mp4", Quality: Quality1080p},
			wantVerdict: TVCompatCompatible,
		},
		{
			name:        "DTS audio is flagged on the safe profile",
			profile:     TVProfileSafe,
			tags:        MediaTags{Codec: "x264", Audio: "DTS", Container: "mp4", Quality: Quality1080p},
			wantVerdict: TVCompatIncompatible,
			wantReason:  "DTS audio",
		},
		{
			name:        "DTS-HD is flagged on the capable profile too (SPEC §8)",
			profile:     TVProfileCapable,
			tags:        MediaTags{Codec: "x265", BitDepth: 10, Audio: "DTS-HD", Container: "mkv", Quality: Quality2160p},
			wantVerdict: TVCompatIncompatible,
			wantReason:  "DTS-HD audio",
		},
		{
			name:        "HEVC is flagged on an H.264-only profile",
			profile:     TVProfileSafe,
			tags:        MediaTags{Codec: "hevc", Audio: "AAC", Container: "mp4", Quality: Quality1080p},
			wantVerdict: TVCompatIncompatible,
			wantReason:  "HEVC video (target allows H.264)",
		},
		{
			name:        "x265 is the same family as HEVC",
			profile:     TVProfileSafe,
			tags:        MediaTags{Codec: "x265"},
			wantVerdict: TVCompatIncompatible,
			wantReason:  "HEVC video",
		},
		{
			name:        "HEVC clears the capable profile",
			profile:     TVProfileCapable,
			tags:        MediaTags{Codec: "x265", BitDepth: 10, Audio: "EAC3", Container: "mkv", Quality: Quality2160p},
			wantVerdict: TVCompatCompatible,
		},
		{
			name:        "AV1 is flagged on safe and clears capable",
			profile:     TVProfileSafe,
			tags:        MediaTags{Codec: "av1", Audio: "Opus"},
			wantVerdict: TVCompatIncompatible,
			wantReason:  "AV1 video",
		},
		{
			name:        "AV1 clears the capable profile",
			profile:     TVProfileCapable,
			tags:        MediaTags{Codec: "av1", Audio: "AAC", Container: "mp4"},
			wantVerdict: TVCompatCompatible,
		},
		{
			name:        "10-bit is flagged on an 8-bit profile",
			profile:     TVProfileSafe,
			tags:        MediaTags{Codec: "x264", BitDepth: 10, Audio: "AAC", Container: "mp4"},
			wantVerdict: TVCompatIncompatible,
			wantReason:  "10-bit video (target allows 8-bit)",
		},
		{
			name:        "10-bit clears the capable profile",
			profile:     TVProfileCapable,
			tags:        MediaTags{Codec: "hevc", BitDepth: 10, Audio: "AC3", Container: "mkv"},
			wantVerdict: TVCompatCompatible,
		},
		{
			name:        "an unstated bit depth is never a complaint",
			profile:     TVProfileSafe,
			tags:        MediaTags{Codec: "x264", Audio: "AAC", Container: "mp4"},
			wantVerdict: TVCompatCompatible,
		},
		{
			name:         "MKV alone is a remux, not a re-encode",
			profile:      TVProfileSafe,
			tags:         MediaTags{Codec: "x264", BitDepth: 8, Audio: "AAC", Container: "mkv", Quality: Quality720p},
			wantVerdict:  TVCompatNeedsRemux,
			wantReason:   "MKV container (target allows MP4/M4V)",
			wantNoReason: "video",
		},
		{
			name:        "MKV clears the capable profile",
			profile:     TVProfileCapable,
			tags:        MediaTags{Codec: "x264", Audio: "AAC", Container: "mkv"},
			wantVerdict: TVCompatCompatible,
		},
		{
			name:        "a stream problem outranks the container",
			profile:     TVProfileSafe,
			tags:        MediaTags{Codec: "hevc", Audio: "DTS", Container: "mkv"},
			wantVerdict: TVCompatIncompatible,
			wantReason:  "MKV container",
		},
		{
			name:        "2160p is flagged on a 1080p profile",
			profile:     TVProfileSafe,
			tags:        MediaTags{Codec: "x264", Audio: "AAC", Container: "mp4", Quality: Quality2160p},
			wantVerdict: TVCompatIncompatible,
			wantReason:  "2160p video (target allows up to 1080p)",
		},
		{
			name:        "a lower resolution than the maximum is fine",
			profile:     TVProfileSafe,
			tags:        MediaTags{Codec: "x264", Audio: "AAC", Container: "mp4", Quality: Quality480p},
			wantVerdict: TVCompatCompatible,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveTVProfile(tt.profile).Check(tt.tags)
			if got.Verdict != tt.wantVerdict {
				t.Errorf("Check(%+v).Verdict = %q, want %q (reasons %v)", tt.tags, got.Verdict, tt.wantVerdict, got.Reasons)
			}
			joined := strings.Join(got.Reasons, "; ")
			if tt.wantReason != "" && !strings.Contains(joined, tt.wantReason) {
				t.Errorf("Check(%+v).Reasons = %q, want it to contain %q", tt.tags, joined, tt.wantReason)
			}
			if tt.wantNoReason != "" && strings.Contains(joined, tt.wantNoReason) {
				t.Errorf("Check(%+v).Reasons = %q, want it NOT to contain %q", tt.tags, joined, tt.wantNoReason)
			}
			if tt.wantVerdict == TVCompatCompatible || tt.wantVerdict == TVCompatUnknown {
				if len(got.Reasons) != 0 {
					t.Errorf("Check(%+v).Reasons = %v, want none for a %s verdict", tt.tags, got.Reasons, tt.wantVerdict)
				}
			}
			if got.Reasons == nil {
				t.Errorf("Check(%+v).Reasons is nil, want an empty slice (it is serialized as JSON)", tt.tags)
			}
		})
	}
}
