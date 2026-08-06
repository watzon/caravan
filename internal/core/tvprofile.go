package core

import (
	"fmt"
	"strings"
)

// TV profiles (SPEC §8) describe what the set on the other end of the library
// can actually decode. Playback display remains descriptive by default: the
// verdict is shown next to a release and next to an imported file. An explicit
// quality-profile policy may also use this verdict while it scores or requires
// acquisition compatibility.
//
// The profiles are built in and code-owned rather than rows in a table: they
// are a fixed vocabulary in v1 (same reasoning as QualityLadder), and the
// active choice is a single settings key, so "delete caravan.db, rescan" costs
// at most the profile selection.

// TV-compatibility verdicts, returned by TVProfile.Check.
const (
	// TVCompatUnknown means the tags carried nothing to judge. It is never a
	// complaint: an untagged release is unknown, not bad.
	TVCompatUnknown = "unknown"
	// TVCompatCompatible means nothing in the known tags conflicts with the
	// profile. It is a statement about the tags, not a guarantee about the
	// bytes — only a probe can promise that.
	TVCompatCompatible = "compatible"
	// TVCompatNeedsRemux means only the container is wrong: the streams
	// themselves are fine, so a copy-only remux fixes it.
	TVCompatNeedsRemux = "needs-remux"
	// TVCompatIncompatible means at least one stream would have to be
	// re-encoded (or the file downscaled) to play natively.
	TVCompatIncompatible = "incompatible"
)

// Video codec families. The parser keeps the tag verbatim ("x265" and "hevc"
// stay distinct, SPEC §8); a profile talks about families, because a set that
// decodes HEVC does not care which encoder produced the stream.
const (
	VideoCodecH264  = "h264"
	VideoCodecHEVC  = "hevc"
	VideoCodecAV1   = "av1"
	VideoCodecVP9   = "vp9"
	VideoCodecMPEG4 = "mpeg4"
	VideoCodecMPEG2 = "mpeg2"
)

// videoCodecFamilies maps every tag the parser emits onto its family.
var videoCodecFamilies = map[string]string{
	"x264":  VideoCodecH264,
	"h264":  VideoCodecH264,
	"avc":   VideoCodecH264,
	"x265":  VideoCodecHEVC,
	"h265":  VideoCodecHEVC,
	"hevc":  VideoCodecHEVC,
	"av1":   VideoCodecAV1,
	"vp9":   VideoCodecVP9,
	"xvid":  VideoCodecMPEG4,
	"divx":  VideoCodecMPEG4,
	"mpeg2": VideoCodecMPEG2,
}

// videoCodecLabels are the spellings a human reads in a reason string.
var videoCodecLabels = map[string]string{
	VideoCodecH264:  "H.264",
	VideoCodecHEVC:  "HEVC",
	VideoCodecAV1:   "AV1",
	VideoCodecVP9:   "VP9",
	VideoCodecMPEG4: "MPEG-4",
	VideoCodecMPEG2: "MPEG-2",
}

// VideoCodecFamily maps a parsed codec tag onto its family, or "" when the tag
// is empty or unrecognized. An unrecognized tag is deliberately not an error:
// it means "we cannot judge this", which is what keeps a new codec out of the
// false-positive column.
func VideoCodecFamily(tag string) string {
	return videoCodecFamilies[strings.ToLower(strings.TrimSpace(tag))]
}

// TVProfile is one named target-set capability description.
type TVProfile struct {
	// ID is the stable slug persisted in settings.
	ID string
	// Name is the picker label.
	Name string
	// Description is the one-line explanation shown under the label.
	Description string
	// VideoCodecs are the accepted families (VideoCodec* constants). Empty
	// means "do not judge video codecs".
	VideoCodecs []string
	// MaxBitDepth is the deepest video the set decodes (8 or 10). Zero means
	// "do not judge bit depth".
	MaxBitDepth int
	// AudioCodecs are the accepted audio tags, spelled exactly as the parser
	// emits them ("AAC", "AC3", "EAC3"). Empty means "do not judge audio".
	AudioCodecs []string
	// Containers are the accepted container extensions, lowercase and without
	// the dot. Empty means "do not judge the container".
	Containers []string
	// MaxQuality is the highest resolution the set displays, one of the
	// Quality* constants. Empty means "do not judge resolution".
	MaxQuality string
}

// MediaTags are the technical claims about one release or one imported file:
// whatever the release parser tagged, plus the container taken from the
// filename. Every field is optional — "" and 0 mean "not stated".
type MediaTags struct {
	// Codec is the parsed video codec tag ("x265", "hevc", …).
	Codec string
	// BitDepth is 8 or 10, or 0 when the name did not say.
	BitDepth int
	// Audio is the parsed audio tag ("DTS", "AAC", …).
	Audio string
	// Container is the lowercase container extension without the dot ("mkv"),
	// as returned by parse.Container.
	Container string
	// Quality is one of the Quality* constants.
	Quality string
}

// TVCompatibility is the verdict for one set of tags against one profile.
type TVCompatibility struct {
	// Verdict is one of the TVCompat* constants.
	Verdict string
	// Reasons are human-readable, worst first. Empty for a compatible or
	// unknown verdict.
	Reasons []string
}

// TVProfileSafe is the default: the common denominator SPEC §8 names, which
// every current set decodes.
const TVProfileSafe = "safe"

// TVProfileCapable covers the modern sets that handle HEVC Main10, AV1 and
// Dolby Digital. DTS stays out of it: SPEC §8 records that current Samsung
// sets cannot decode it at all and that it is flaky elsewhere.
const TVProfileCapable = "capable"

var tvProfiles = []TVProfile{
	{
		ID:          TVProfileSafe,
		Name:        "Safe — H.264 8-bit + AAC in MP4, up to 1080p",
		Description: "The common denominator every current TV decodes without help. Pick this when you do not know what the set can do.",
		VideoCodecs: []string{VideoCodecH264},
		MaxBitDepth: 8,
		AudioCodecs: []string{"AAC"},
		Containers:  []string{"mp4", "m4v"},
		MaxQuality:  Quality1080p,
	},
	{
		ID:          TVProfileCapable,
		Name:        "Capable — HEVC Main10 / AV1 + AC3, up to 2160p",
		Description: "A modern set: 10-bit HEVC, AV1 and Dolby Digital in MKV or MP4. DTS is still flagged — current Samsung sets cannot decode it.",
		VideoCodecs: []string{VideoCodecH264, VideoCodecHEVC, VideoCodecAV1},
		MaxBitDepth: 10,
		AudioCodecs: []string{"AAC", "AC3", "EAC3"},
		Containers:  []string{"mp4", "m4v", "mkv"},
		MaxQuality:  Quality2160p,
	},
}

// TVProfiles returns the built-in profiles, best-known-to-work first.
func TVProfiles() []TVProfile {
	out := make([]TVProfile, len(tvProfiles))
	copy(out, tvProfiles)
	return out
}

// ResolveTVProfile returns the profile with the given id. An unset or unknown
// id resolves to the safe default rather than to an error: a settings row that
// names a profile a later build removed must not break the release picker.
func ResolveTVProfile(id string) TVProfile {
	for _, p := range tvProfiles {
		if p.ID == id {
			return p
		}
	}
	return tvProfiles[0]
}

// Check reports whether tags would play natively on this profile.
//
// Only stated tags are judged; a missing tag is never held against a release.
// Container problems are separated from stream problems because they have
// wildly different costs: a remux is a copy, a re-encode is not.
func (p TVProfile) Check(tags MediaTags) TVCompatibility {
	var hard, remux []string
	judged := 0

	if family := VideoCodecFamily(tags.Codec); family != "" && len(p.VideoCodecs) > 0 {
		judged++
		if !containsFold(p.VideoCodecs, family) {
			hard = append(hard, fmt.Sprintf("%s video (profile allows %s)",
				videoCodecLabel(family), joinLabels(p.VideoCodecs, videoCodecLabel)))
		}
	}
	if tags.BitDepth > 0 && p.MaxBitDepth > 0 {
		judged++
		if tags.BitDepth > p.MaxBitDepth {
			hard = append(hard, fmt.Sprintf("%d-bit video (profile allows %d-bit)", tags.BitDepth, p.MaxBitDepth))
		}
	}
	if audio := strings.TrimSpace(tags.Audio); audio != "" && len(p.AudioCodecs) > 0 {
		judged++
		if !containsFold(p.AudioCodecs, audio) {
			hard = append(hard, fmt.Sprintf("%s audio (profile allows %s)",
				audio, strings.Join(p.AudioCodecs, "/")))
		}
	}
	// Resolution is judged but never counts as "we know something": a
	// resolution alone says nothing about whether the streams decode, so it
	// can condemn a release without being able to clear one.
	if tags.Quality != "" && tags.Quality != QualityUnknown && p.MaxQuality != "" &&
		QualityRank(tags.Quality) < QualityRank(p.MaxQuality) {
		hard = append(hard, fmt.Sprintf("%s video (profile allows up to %s)", tags.Quality, p.MaxQuality))
	}
	if container := strings.ToLower(strings.TrimSpace(tags.Container)); container != "" && len(p.Containers) > 0 {
		judged++
		if !containsFold(p.Containers, container) {
			remux = append(remux, fmt.Sprintf("%s container (profile allows %s)",
				strings.ToUpper(container), joinLabels(p.Containers, strings.ToUpper)))
		}
	}

	switch {
	case len(hard) > 0:
		return TVCompatibility{Verdict: TVCompatIncompatible, Reasons: append(hard, remux...)}
	case len(remux) > 0:
		return TVCompatibility{Verdict: TVCompatNeedsRemux, Reasons: remux}
	case judged == 0:
		return TVCompatibility{Verdict: TVCompatUnknown, Reasons: []string{}}
	}
	return TVCompatibility{Verdict: TVCompatCompatible, Reasons: []string{}}
}

func videoCodecLabel(family string) string {
	if label, ok := videoCodecLabels[family]; ok {
		return label
	}
	return family
}

func joinLabels(values []string, label func(string) string) string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, label(v))
	}
	return strings.Join(out, "/")
}

func containsFold(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}
