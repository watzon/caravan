package convert

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// Probe is what ffprobe reports about one file: the technical claims the
// container itself makes, as opposed to the ones a release name makes.
//
// Every field is optional in the same way core.MediaTags' are — a stream ffprobe
// could not describe is unstated, not wrong.
type Probe struct {
	// Duration is the container duration in seconds, 0 when unstated.
	Duration float64
	// VideoCodec is the ffprobe codec name of the first video stream
	// ("h264", "hevc", "av1"…), empty when there is no video stream.
	VideoCodec string
	// BitDepth is 8, 10 or 12, derived from the pixel format; 0 when unstated.
	BitDepth int
	// Width and Height are the coded dimensions of the first video stream.
	Width, Height int
	// AudioCodecs are the ffprobe codec names of every audio stream in order
	// ("aac", "ac3", "dts"…), empty when there is no audio stream.
	//
	// The first one is what the profile judges — it is the track a set plays —
	// but the rest are kept because they are the streams the mux has to accept:
	// copying a TrueHD commentary track into MP4 fails outright, and a plan
	// that never looked at it could not know that.
	AudioCodecs []string
}

// AudioCodec is the first audio stream's codec name, or empty when the file has
// no audio. It is the one the profile judges.
func (p Probe) AudioCodec() string {
	if len(p.AudioCodecs) == 0 {
		return ""
	}
	return p.AudioCodecs[0]
}

// Plan is the decision for one file: what to run, into what, and why.
type Plan struct {
	// Strategy is one of the core.ConvertStrategy* constants.
	Strategy string
	// Container is the target container extension without the dot. Empty when
	// Strategy is none.
	Container string
	// MaxHeight caps a transcode's output height; 0 means "do not scale".
	MaxHeight int
	// VideoCopy and AudioCopy say which streams a transcode may stream-copy
	// rather than re-encode. They are only consulted for a transcode: a remux
	// copies everything by definition, and their zero value is therefore the
	// conservative one (re-encode).
	VideoCopy, AudioCopy bool
	// AudioStreams are the audio stream indices to keep, in probe order, when
	// the audio is copied. nil means "every audio stream", which is what a
	// re-encode wants: everything it maps comes out as AAC and muxes.
	AudioStreams []int
	// Reasons are the profile's complaints, worst first — the same strings the
	// compatibility badge shows, so the queue and the badge never disagree.
	Reasons []string
}

// Decide picks remux, transcode or nothing for a probed file.
//
// The choice is the TV profile's verdict, not a second opinion: a container
// problem is a stream copy and a stream problem is a re-encode, which is
// exactly the needs-remux/incompatible split core.TVProfile.Check already
// draws. SPEC §8 wants the cheap path tried first, and this is what makes
// "first" mean "whenever it is sufficient" rather than "always attempted".
//
// "Cheap first" applies per stream, not just per file. A 1080p H.264 file whose
// only problem is DTS audio is incompatible, but re-encoding its video would
// cost hours of CPU and a generation of quality to fix a stream that was never
// the complaint: each stream is asked about on its own, and only the ones the
// profile actually rejects are re-encoded.
func Decide(profile core.TVProfile, p Probe, container string) Plan {
	tags := ProbeTags(p, container)
	compat := profile.Check(tags)
	plan := Plan{Reasons: compat.Reasons, Container: targetContainer(profile)}

	switch compat.Verdict {
	case core.TVCompatNeedsRemux:
		plan.Strategy = core.ConvertStrategyRemux
		plan.AudioStreams = muxableAudio(profile, p)
	case core.TVCompatIncompatible:
		plan.Strategy = core.ConvertStrategyTranscode
		plan.VideoCopy = !rejects(profile, core.MediaTags{
			Codec: tags.Codec, BitDepth: tags.BitDepth, Quality: tags.Quality,
		})
		plan.AudioCopy = !rejects(profile, core.MediaTags{Audio: tags.Audio})
		if !plan.VideoCopy {
			// Only a re-encoded video stream can be scaled; a copied one comes
			// out at the resolution it went in at.
			plan.MaxHeight = qualityHeight(profile.MaxQuality)
		}
		if plan.AudioCopy {
			plan.AudioStreams = muxableAudio(profile, p)
		}
	default:
		// Compatible and unknown both mean "do not touch it". Unknown is the
		// important one: re-encoding a file we could not judge would destroy
		// quality on a guess.
		plan.Strategy = core.ConvertStrategyNone
		plan.Container = ""
	}
	return plan
}

// rejects reports whether the profile has a hard complaint about this much of a
// file. It asks the profile rather than re-deriving its rules, so a per-stream
// decision can never disagree with the verdict the badge shows.
func rejects(profile core.TVProfile, tags core.MediaTags) bool {
	return profile.Check(tags).Verdict == core.TVCompatIncompatible
}

// muxableAudio is the audio stream indices a stream copy may keep: the ones the
// profile accepts.
//
// Everything else is dropped for the same reason subtitles are — the target
// container has no tag for it and the mux would fail — and dropping a track the
// set could not have decoded costs the user nothing. A probe that reported no
// audio at all yields nil, which Args reads as "whatever is there".
func muxableAudio(profile core.TVProfile, p Probe) []int {
	if len(p.AudioCodecs) == 0 {
		return nil
	}
	keep := []int{}
	for i, codec := range p.AudioCodecs {
		if !rejects(profile, core.MediaTags{Audio: audioTag(codec)}) {
			keep = append(keep, i)
		}
	}
	return keep
}

// ProbeTags turns a probe into the tag shape the TV profile judges. It is the
// bridge between ffprobe's vocabulary and the release parser's, which is the
// vocabulary the profiles are written in (SPEC §8 keeps audio tags verbatim).
func ProbeTags(p Probe, container string) core.MediaTags {
	return core.MediaTags{
		Codec:     videoTag(p.VideoCodec),
		BitDepth:  p.BitDepth,
		Audio:     audioTag(p.AudioCodec()),
		Container: strings.ToLower(strings.TrimSpace(container)),
		Quality:   qualityOf(p.Width, p.Height),
	}
}

// ffprobeVideoTags maps ffprobe codec names onto tags core.VideoCodecFamily
// understands. Only the names that differ are listed; h264, hevc, av1 and vp9
// are spelled the same on both sides.
var ffprobeVideoTags = map[string]string{
	"mpeg2video": "mpeg2",
	"mpeg4":      "xvid",
	"msmpeg4v3":  "divx",
}

func videoTag(codec string) string {
	codec = strings.ToLower(strings.TrimSpace(codec))
	if tag, ok := ffprobeVideoTags[codec]; ok {
		return tag
	}
	return codec
}

// ffprobeAudioTags maps ffprobe codec names onto the parser's spellings, which
// is what a profile's AudioCodecs list is written in.
var ffprobeAudioTags = map[string]string{
	"aac":    "AAC",
	"ac3":    "AC3",
	"eac3":   "EAC3",
	"dts":    "DTS",
	"truehd": "TrueHD",
	"flac":   "FLAC",
	"opus":   "Opus",
	"mp3":    "MP3",
	"vorbis": "Vorbis",
}

// audioTag normalizes one ffprobe audio codec name.
//
// An unrecognized name is passed through uppercased rather than dropped: an
// unknown audio codec that a profile does not list reads as incompatible,
// which errs towards offering a conversion the user can decline instead of
// silently declaring a file fine.
func audioTag(codec string) string {
	codec = strings.ToLower(strings.TrimSpace(codec))
	if codec == "" {
		return ""
	}
	if tag, ok := ffprobeAudioTags[codec]; ok {
		return tag
	}
	if strings.HasPrefix(codec, "pcm_") {
		return "PCM"
	}
	return strings.ToUpper(codec)
}

// bitDepthFromPixFmt reads the bit depth out of an ffprobe pixel format
// ("yuv420p" → 8, "yuv420p10le" → 10). An unrecognized format yields 0, which
// the profile check treats as unstated.
func bitDepthFromPixFmt(pixFmt string) int {
	pixFmt = strings.ToLower(strings.TrimSpace(pixFmt))
	if pixFmt == "" {
		return 0
	}
	for _, depth := range []int{16, 14, 12, 10, 9} {
		if strings.Contains(pixFmt, strconv.Itoa(depth)) {
			return depth
		}
	}
	// Every 8-bit format ffmpeg emits spells its depth nowhere, so "no digits"
	// is the 8-bit case rather than the unknown one.
	return 8
}

// quality maps coded dimensions onto a core.Quality* rung.
//
// Width decides, not height: a 3840x1600 scope-ratio 4K file is 2160p to every
// human who looks at it, and judging it by its letterboxed height would file it
// as 1080p and hide a downscale the profile actually needs.
func qualityOf(width, height int) string {
	if width <= 0 && height <= 0 {
		return ""
	}
	if width <= 0 {
		// Vertical-only information: fall back to the nominal heights.
		width = height * 16 / 9
	}
	switch {
	case width >= 3000:
		return core.Quality2160p
	case width >= 1800:
		return core.Quality1080p
	case width >= 1200:
		return core.Quality720p
	default:
		return core.Quality480p
	}
}

// qualityHeights are the output heights a transcode caps at, per profile rung.
var qualityHeights = map[string]int{
	core.Quality2160p: 2160,
	core.Quality1080p: 1080,
	core.Quality720p:  720,
	core.Quality480p:  480,
}

func qualityHeight(q string) int { return qualityHeights[q] }

// targetContainer is what a conversion writes into: MP4 when the profile
// accepts it, otherwise the profile's first choice. MP4 is preferred because
// it is the container every profile in SPEC §8 accepts and the only one
// +faststart means anything in.
func targetContainer(profile core.TVProfile) string {
	if len(profile.Containers) == 0 {
		return "mp4"
	}
	for _, c := range profile.Containers {
		if strings.EqualFold(c, "mp4") {
			return "mp4"
		}
	}
	return strings.ToLower(profile.Containers[0])
}

// Args renders the ffmpeg command line for a plan and one snapshot of the
// global encoding settings.
// Notes on the choices, since they are the difference between "a file that
// plays" and "a file that almost plays":
//
//   - -map 0:v:0 takes the first video stream, and the audio maps take either
//     everything or the streams Plan.AudioStreams named. Subtitles are
//     deliberately dropped: an MKV's PGS or SSA subtitles have no MP4
//     equivalent and would fail the mux outright.
//   - -movflags +faststart moves the index to the front, which is what lets a
//     TV start playing before the whole file has been read.
//   - a re-encoded video stream targets H.264 High 8-bit and a re-encoded audio
//     stream targets AAC, because that is the profile floor SPEC §8 names;
//     yuv420p is forced because a 10-bit source would otherwise produce a
//     10-bit H.264 stream no set decodes.
func Args(plan Plan, settings EncodingSettings, in, out string) []string {
	args := []string{"-nostdin", "-y", "-i", in, "-map", "0:v:0"}
	if plan.AudioStreams == nil {
		args = append(args, "-map", "0:a?")
	}
	for _, i := range plan.AudioStreams {
		args = append(args, "-map", fmt.Sprintf("0:a:%d", i))
	}

	if plan.Strategy == core.ConvertStrategyRemux {
		args = append(args, "-c", "copy")
		return append(args, "-movflags", "+faststart", out)
	}

	if plan.VideoCopy {
		args = append(args, "-c:v", "copy")
	} else {
		args = append(args,
			"-c:v", "libx264",
			"-preset", settings.VideoPreset,
			"-crf", strconv.Itoa(settings.VideoCRF),
			"-profile:v", "high", "-pix_fmt", "yuv420p")
		if plan.MaxHeight > 0 {
			// -2 keeps the width even (H.264 requires it) and preserves the
			// aspect ratio; min() leaves anything already small enough alone,
			// so a conversion never upscales.
			args = append(args, "-vf", fmt.Sprintf("scale=-2:min(ih\\,%d)", plan.MaxHeight))
		}
	}
	if plan.AudioCopy {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-b:a", strconv.Itoa(settings.AudioBitrateKbps)+"k")
	}
	return append(args, "-movflags", "+faststart", out)
}

// durationTolerance is how far the output may drift from the source before the
// conversion is called a failure: two seconds, or 2% for long files, whichever
// is larger. A remux is exact; a transcode rounds at the last GOP boundary.
func durationTolerance(seconds float64) float64 {
	return math.Max(2, seconds*0.02)
}

// Verify decides whether the file ffmpeg produced may replace the original.
//
// It is deliberately paranoid about the one failure that costs media: an
// ffmpeg that exits 0 having written a truncated file. Size, decodability and
// duration are each checked, because a truncated MP4 can still be non-empty
// and still parse.
func Verify(source, output Probe, outputSize int64) error {
	if outputSize <= 0 {
		return fmt.Errorf("converted file is empty")
	}
	if output.VideoCodec == "" {
		return fmt.Errorf("converted file has no video stream")
	}
	if output.Duration <= 0 {
		return fmt.Errorf("converted file reports no duration")
	}
	if source.Duration > 0 {
		if drift := math.Abs(source.Duration - output.Duration); drift > durationTolerance(source.Duration) {
			return fmt.Errorf("converted file is %.0fs long, source is %.0fs",
				output.Duration, source.Duration)
		}
	}
	return nil
}
