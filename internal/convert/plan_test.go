package convert

import (
	"slices"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestDecideRemuxesWhenOnlyTheContainerIsWrong(t *testing.T) {
	profile := core.ResolveTVProfile(core.TVProfileSafe)
	// H.264 8-bit + AAC at 1080p: every stream the safe profile accepts, in a
	// container it does not.
	probe := Probe{Duration: 3600, VideoCodec: "h264", BitDepth: 8, Width: 1920, Height: 1080, AudioCodecs: []string{"aac"}}

	plan := Decide(profile, probe, "mkv")

	if plan.Strategy != core.ConvertStrategyRemux {
		t.Fatalf("strategy = %q, want remux (reasons %v)", plan.Strategy, plan.Reasons)
	}
	if plan.Container != "mp4" {
		t.Fatalf("container = %q, want mp4", plan.Container)
	}
	if plan.MaxHeight != 0 {
		t.Fatalf("max height = %d, want 0: a stream copy must never scale", plan.MaxHeight)
	}
}

// Only the streams the profile actually rejects are re-encoded. Re-encoding a
// video stream to fix an audio codec costs hours of CPU and a generation of
// quality for nothing (SPEC §8: the cheap path is tried first).
func TestDecideTranscodesOnlyTheOffendingStreams(t *testing.T) {
	profile := core.ResolveTVProfile(core.TVProfileSafe)
	tests := []struct {
		name          string
		probe         Probe
		container     string
		wantVideoCopy bool
		wantAudioCopy bool
		wantMaxHeight int
	}{
		{
			name:          "hevc video",
			probe:         Probe{VideoCodec: "hevc", BitDepth: 8, Width: 1920, Height: 1080, AudioCodecs: []string{"aac"}},
			container:     "mp4",
			wantAudioCopy: true,
			wantMaxHeight: 1080,
		},
		{
			name:          "10-bit h264",
			probe:         Probe{VideoCodec: "h264", BitDepth: 10, Width: 1920, Height: 1080, AudioCodecs: []string{"aac"}},
			container:     "mp4",
			wantAudioCopy: true,
			wantMaxHeight: 1080,
		},
		{
			// The case SPEC §8 names: the video is already what the profile
			// wants, so it is copied and nothing is scaled.
			name:          "dts audio",
			probe:         Probe{VideoCodec: "h264", BitDepth: 8, Width: 1920, Height: 1080, AudioCodecs: []string{"dts"}},
			container:     "mp4",
			wantVideoCopy: true,
		},
		{
			name:          "2160p above the profile ceiling",
			probe:         Probe{VideoCodec: "h264", BitDepth: 8, Width: 3840, Height: 2160, AudioCodecs: []string{"aac"}},
			container:     "mp4",
			wantAudioCopy: true,
			wantMaxHeight: 1080,
		},
		{
			name:          "hevc video and dts audio",
			probe:         Probe{VideoCodec: "hevc", BitDepth: 10, Width: 3840, Height: 2160, AudioCodecs: []string{"dts"}},
			container:     "mkv",
			wantMaxHeight: 1080,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := Decide(profile, tc.probe, tc.container)
			if plan.Strategy != core.ConvertStrategyTranscode {
				t.Fatalf("strategy = %q, want transcode", plan.Strategy)
			}
			if plan.VideoCopy != tc.wantVideoCopy {
				t.Errorf("video copy = %v, want %v", plan.VideoCopy, tc.wantVideoCopy)
			}
			if plan.AudioCopy != tc.wantAudioCopy {
				t.Errorf("audio copy = %v, want %v", plan.AudioCopy, tc.wantAudioCopy)
			}
			// A copied video stream comes out at the resolution it went in at,
			// so a cap on it would be a promise the command cannot keep.
			if plan.MaxHeight != tc.wantMaxHeight {
				t.Errorf("max height = %d, want %d", plan.MaxHeight, tc.wantMaxHeight)
			}
			if len(plan.Reasons) == 0 {
				t.Fatal("a transcode with no reason is a transcode nobody asked for")
			}
		})
	}
}

// The DTS-audio case end to end: the ffmpeg command copies the video and only
// re-encodes the audio, which is the difference between seconds and hours.
func TestDTSAudioOnlyReEncodesTheAudio(t *testing.T) {
	profile := core.ResolveTVProfile(core.TVProfileSafe)
	probe := Probe{Duration: 3600, VideoCodec: "h264", BitDepth: 8, Width: 1920, Height: 1080, AudioCodecs: []string{"dts"}}

	args := Args(Decide(profile, probe, "mkv"), "/in.mkv", "/out.mp4")

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c:v copy") {
		t.Errorf("the video stream was re-encoded to fix the audio: %v", args)
	}
	if strings.Contains(joined, "libx264") {
		t.Errorf("a video re-encode leaked into an audio-only conversion: %v", args)
	}
	if !strings.Contains(joined, "-c:a aac") {
		t.Errorf("the audio was not re-encoded: %v", args)
	}
	if slices.Contains(args, "-vf") {
		t.Errorf("a copied video stream cannot be scaled: %v", args)
	}
}

// A stream copy may only map audio the target container can hold. An MP4 muxer
// has no tag for TrueHD, so copying a TrueHD commentary track alongside an
// acceptable first track fails the whole command — a conversion the UI called a
// container swap, failing forever with nothing the user can do about it.
func TestRemuxDropsAudioTheContainerCannotHold(t *testing.T) {
	profile := core.ResolveTVProfile(core.TVProfileSafe)
	probe := Probe{
		Duration: 3600, VideoCodec: "h264", BitDepth: 8, Width: 1920, Height: 1080,
		AudioCodecs: []string{"aac", "truehd", "aac"},
	}

	plan := Decide(profile, probe, "mkv")
	if plan.Strategy != core.ConvertStrategyRemux {
		t.Fatalf("strategy = %q, want remux", plan.Strategy)
	}
	if !slices.Equal(plan.AudioStreams, []int{0, 2}) {
		t.Fatalf("audio streams = %v, want [0 2]: the TrueHD track cannot be copied into MP4", plan.AudioStreams)
	}

	args := Args(plan, "/in.mkv", "/out.mp4")
	joined := strings.Join(args, " ")
	for _, want := range []string{"-map 0:a:0", "-map 0:a:2"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, args)
		}
	}
	if strings.Contains(joined, "-map 0:a:1") || strings.Contains(joined, "-map 0:a?") {
		t.Errorf("args map an unmuxable audio stream: %v", args)
	}
}

func TestDecideSkipsCompatibleAndUnknownFiles(t *testing.T) {
	profile := core.ResolveTVProfile(core.TVProfileSafe)
	tests := []struct {
		name      string
		probe     Probe
		container string
	}{
		{
			name:      "already compatible",
			probe:     Probe{VideoCodec: "h264", BitDepth: 8, Width: 1920, Height: 1080, AudioCodecs: []string{"aac"}},
			container: "mp4",
		},
		{
			// A probe that said nothing is not evidence of a problem, and
			// re-encoding on a guess destroys quality for free.
			name:      "nothing stated",
			probe:     Probe{},
			container: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := Decide(profile, tc.probe, tc.container)
			if plan.Strategy != core.ConvertStrategyNone {
				t.Fatalf("strategy = %q, want none", plan.Strategy)
			}
			if plan.Container != "" {
				t.Fatalf("container = %q, want empty: nothing is being written", plan.Container)
			}
		})
	}
}

func TestDecideRespectsTheCapableProfile(t *testing.T) {
	profile := core.ResolveTVProfile(core.TVProfileCapable)
	// 10-bit HEVC 2160p with AC3: exactly what the capable profile is for.
	probe := Probe{VideoCodec: "hevc", BitDepth: 10, Width: 3840, Height: 2160, AudioCodecs: []string{"ac3"}}

	// MKV is on the capable profile's list, so there is nothing to do.
	if plan := Decide(profile, probe, "mkv"); plan.Strategy != core.ConvertStrategyNone {
		t.Fatalf("mkv strategy = %q, want none", plan.Strategy)
	}
	// AVI is not, and only the container is wrong.
	if plan := Decide(profile, probe, "avi"); plan.Strategy != core.ConvertStrategyRemux {
		t.Fatalf("avi strategy = %q, want remux", plan.Strategy)
	}
	// The same file is a full re-encode for the safe profile: 10-bit HEVC and
	// AC3 both have to go.
	safe := core.ResolveTVProfile(core.TVProfileSafe)
	if plan := Decide(safe, probe, "mkv"); plan.Strategy != core.ConvertStrategyTranscode {
		t.Fatalf("safe-profile strategy = %q, want transcode", plan.Strategy)
	}
}

func TestProbeTagsNormalizesFFprobeNames(t *testing.T) {
	tests := []struct {
		probe     Probe
		wantCodec string
		wantAudio string
		wantQual  string
	}{
		{Probe{VideoCodec: "mpeg2video", AudioCodecs: []string{"pcm_s16le"}, Width: 720, Height: 480}, "mpeg2", "PCM", core.Quality480p},
		{Probe{VideoCodec: "H264", AudioCodecs: []string{"EAC3"}, Width: 1920, Height: 1080}, "h264", "EAC3", core.Quality1080p},
		{Probe{VideoCodec: "av1", AudioCodecs: []string{"truehd"}, Width: 1280, Height: 720}, "av1", "TrueHD", core.Quality720p},
		// Scope-ratio 4K: judged by width, so the letterboxing does not
		// disguise it as 1080p.
		{Probe{VideoCodec: "hevc", AudioCodecs: []string{"flac"}, Width: 3840, Height: 1600}, "hevc", "FLAC", core.Quality2160p},
		// An audio codec nothing recognizes still says something, and what it
		// says must not be "fine".
		{Probe{VideoCodec: "vp9", AudioCodecs: []string{"ralf"}, Width: 1920, Height: 1080}, "vp9", "RALF", core.Quality1080p},
	}
	for _, tc := range tests {
		got := ProbeTags(tc.probe, "mkv")
		if got.Codec != tc.wantCodec || got.Audio != tc.wantAudio || got.Quality != tc.wantQual {
			t.Errorf("ProbeTags(%+v) = codec %q audio %q quality %q; want %q/%q/%q",
				tc.probe, got.Codec, got.Audio, got.Quality, tc.wantCodec, tc.wantAudio, tc.wantQual)
		}
	}
}

func TestBitDepthFromPixFmt(t *testing.T) {
	tests := map[string]int{
		"yuv420p":     8,
		"yuvj420p":    8,
		"yuv420p10le": 10,
		"p010le":      10,
		"yuv444p12le": 12,
		"":            0,
	}
	for pixFmt, want := range tests {
		if got := bitDepthFromPixFmt(pixFmt); got != want {
			t.Errorf("bitDepthFromPixFmt(%q) = %d, want %d", pixFmt, got, want)
		}
	}
}

func TestArgsRemuxCopiesStreams(t *testing.T) {
	args := Args(Plan{Strategy: core.ConvertStrategyRemux, Container: "mp4"}, "/in.mkv", "/out.mp4")

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c copy") {
		t.Fatalf("remux args do not copy streams: %v", args)
	}
	if strings.Contains(joined, "libx264") {
		t.Fatalf("remux args re-encode: %v", args)
	}
	if !strings.Contains(joined, "-movflags +faststart") {
		t.Fatalf("remux args do not move the index to the front: %v", args)
	}
	if args[len(args)-1] != "/out.mp4" {
		t.Fatalf("output must be the last argument: %v", args)
	}
	if !slices.Contains(args, "/in.mkv") {
		t.Fatalf("input is missing: %v", args)
	}
}

func TestArgsTranscodeTargetsTheProfileFloor(t *testing.T) {
	args := Args(Plan{Strategy: core.ConvertStrategyTranscode, Container: "mp4", MaxHeight: 1080}, "/in.mkv", "/out.mp4")

	joined := strings.Join(args, " ")
	for _, want := range []string{"libx264", "-pix_fmt yuv420p", "-c:a aac", "scale=-2:min(ih\\,1080)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("transcode args missing %q: %v", want, args)
		}
	}
	if strings.Contains(joined, "-c copy") {
		t.Fatalf("a transcode must not stream-copy: %v", args)
	}
}

func TestArgsTranscodeWithoutACapDoesNotScale(t *testing.T) {
	args := Args(Plan{Strategy: core.ConvertStrategyTranscode, Container: "mp4"}, "/in.mkv", "/out.mp4")
	if slices.Contains(args, "-vf") {
		t.Fatalf("no height cap must mean no scale filter: %v", args)
	}
}

func TestVerifyRejectsBadOutput(t *testing.T) {
	source := Probe{Duration: 3600, VideoCodec: "h264"}
	good := Probe{Duration: 3600, VideoCodec: "h264"}

	if err := Verify(source, good, 1<<20); err != nil {
		t.Fatalf("a matching output must verify: %v", err)
	}
	// A transcode rounds at the last GOP boundary; a second of drift is fine.
	if err := Verify(source, Probe{Duration: 3599.2, VideoCodec: "h264"}, 1<<20); err != nil {
		t.Fatalf("sub-tolerance drift must verify: %v", err)
	}

	tests := []struct {
		name   string
		output Probe
		size   int64
	}{
		{"empty file", good, 0},
		{"no video stream", Probe{Duration: 3600}, 1 << 20},
		{"no duration", Probe{VideoCodec: "h264"}, 1 << 20},
		// The failure that costs media: ffmpeg exits 0 having written a
		// truncated file that still parses.
		{"truncated", Probe{Duration: 120, VideoCodec: "h264"}, 1 << 20},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := Verify(source, tc.output, tc.size); err == nil {
				t.Fatal("want an error, got none")
			}
		})
	}
}

func TestParseProbeReadsFFprobeJSON(t *testing.T) {
	const report = `{
	  "streams": [
	    {"codec_type": "video", "codec_name": "hevc", "pix_fmt": "yuv420p10le", "width": 3840, "height": 2160},
	    {"codec_type": "audio", "codec_name": "dts"},
	    {"codec_type": "audio", "codec_name": "aac"},
	    {"codec_type": "subtitle", "codec_name": "subrip"}
	  ],
	  "format": {"duration": "7200.512000"}
	}`

	p, err := ParseProbe([]byte(report))
	if err != nil {
		t.Fatalf("ParseProbe: %v", err)
	}
	if p.VideoCodec != "hevc" || p.BitDepth != 10 || p.Width != 3840 || p.Height != 2160 {
		t.Errorf("video = %+v", p)
	}
	// The first audio track is the one a set plays, and therefore the one the
	// profile judges — but every track is kept, because a stream copy has to
	// mux all the ones it maps.
	if p.AudioCodec() != "dts" {
		t.Errorf("audio codec = %q, want dts", p.AudioCodec())
	}
	if !slices.Equal(p.AudioCodecs, []string{"dts", "aac"}) {
		t.Errorf("audio codecs = %v, want [dts aac]", p.AudioCodecs)
	}
	if p.Duration < 7200 || p.Duration > 7201 {
		t.Errorf("duration = %v, want ~7200.5", p.Duration)
	}
}

func TestParseProbeToleratesMissingDuration(t *testing.T) {
	p, err := ParseProbe([]byte(`{"streams":[{"codec_type":"video","codec_name":"h264","pix_fmt":"yuv420p"}],"format":{"duration":"N/A"}}`))
	if err != nil {
		t.Fatalf("ParseProbe: %v", err)
	}
	if p.Duration != 0 {
		t.Fatalf("duration = %v, want 0 for an unstated duration", p.Duration)
	}
	if p.BitDepth != 8 {
		t.Fatalf("bit depth = %d, want 8", p.BitDepth)
	}
}
