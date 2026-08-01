package core

// Quality values. This is a fixed ladder for v1: SPEC §16 puts custom formats
// out of scope, so every quality in Caravan is one of these strings.
const (
	Quality2160p   = "2160p"
	Quality1080p   = "1080p"
	Quality720p    = "720p"
	Quality480p    = "480p"
	QualityUnknown = "unknown"
)

// QualityLadder lists the known qualities best-first. QualityUnknown is
// deliberately excluded: it is the parser's "I could not tell" answer, not a
// rung a user can select or cut off at.
var QualityLadder = []string{Quality2160p, Quality1080p, Quality720p, Quality480p}

// QualityRank returns the position of q in QualityLadder, where a lower number
// is better. Unknown or unrecognized qualities rank below every known rung so
// that "anything identifiable beats a mystery file" holds without special
// cases at the call site.
func QualityRank(q string) int {
	for i, known := range QualityLadder {
		if known == q {
			return i
		}
	}
	return len(QualityLadder)
}

// Source values: how a release was captured. Same fixed-ladder reasoning as
// quality, ordered best-first in SourceLadder.
const (
	SourceBluray  = "bluray"
	SourceWebDL   = "webdl"
	SourceWebRip  = "webrip"
	SourceHDTV    = "hdtv"
	SourceDVD     = "dvd"
	SourceCam     = "cam"
	SourceUnknown = "unknown"
)

// SourceLadder lists the known sources best-first. SourceUnknown is excluded
// for the same reason as QualityUnknown.
var SourceLadder = []string{SourceBluray, SourceWebDL, SourceWebRip, SourceHDTV, SourceDVD, SourceCam}

// SourceRank returns the position of s in SourceLadder, lower being better.
// Unknown or unrecognized sources rank below every known source.
func SourceRank(s string) int {
	for i, known := range SourceLadder {
		if known == s {
			return i
		}
	}
	return len(SourceLadder)
}
