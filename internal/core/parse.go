package core

import "time"

// ParsedRelease is what the release-name parser (internal/parse) extracts from
// a scene name or an existing filename. It carries no I/O and no database
// identity: it is a pure description of what a name claims to be.
type ParsedRelease struct {
	// Title is the release title with separators normalized to spaces and
	// scene noise stripped.
	Title string
	// Year is the release year, or 0 when the name did not carry one.
	Year int
	// Season is the season number for episode releases, or 0 for movies.
	Season int
	// Episodes holds every episode number the release covers, ascending. A
	// multi-episode file (S01E01E02) yields more than one entry; a movie
	// yields none.
	Episodes []int
	// Absolute is the absolute (series-wide) episode number an anime-style
	// release name carries — "Show - 105" — and is zero on every other name.
	// It is never set alongside Episodes: a name that says S05E03 has already
	// said which episode it is, and a second, differently-derived answer to the
	// same question is only ever a chance to disagree.
	//
	// It is a number rather than a (season, episode) pair because the mapping
	// from an absolute number to a season is a fact about the SERIES, not about
	// the filename — exactly as SceneDate's mapping is a fact about the site.
	// The name claims "the 105th episode"; which season that lands in depends
	// on how long the earlier seasons ran, which the name does not say and
	// cannot. The library layer resolves it once it knows which series the file
	// belongs to.
	Absolute int `json:",omitempty"`
	// Quality is one of the Quality* constants.
	Quality string
	// Source is one of the Source* constants.
	Source string
	// Codec is the video codec tag as parsed ("x265", "h264", …), or empty.
	Codec string
	// Audio is the audio tag as parsed ("DTS", "AAC", …), or empty.
	Audio string
	// BitDepth is the video bit depth the name claims (8 or 10), or 0 when it
	// claimed none. SPEC §8: a Main10 stream is exactly what an older set
	// refuses, so the TV-profile check needs this separate from Codec.
	BitDepth int
	// Group is the release group, or empty.
	Group string
	// Proper marks a PROPER re-release.
	Proper bool
	// Repack marks a REPACK re-release.
	Repack bool
	// Edition is free-text movie edition ("Director's Cut", "Extended"), or
	// empty. SPEC §7 keeps this unstructured for v1.
	Edition string
	// SceneDate is the release date a date-based adult release name carries
	// ("Site.22.03.14.…"), zero on every other name. Only parse.Scene ever
	// sets it (PLAN phase 9 task 4).
	//
	// It is a date rather than an episode number because the number an adult
	// release lands on — the scene's sequence within its release year — is not
	// in the filename and cannot be: it is a fact about the site's whole
	// catalogue. The date is what the name actually claims, and the library
	// layer turns it into (season, episode) once it knows which site the file
	// belongs to.
	SceneDate time.Time `json:",omitempty"`
	// Confidence is the parser's self-assessment in [0,1]. Low-confidence
	// results are what park files in the unmatched queue rather than being
	// imported silently.
	Confidence float64
}

// IsEpisode reports whether the name named a season and an episode. It is
// deliberately not widened to cover absolute numbering: every call site reads
// it as "Season and Episodes are usable right now", and a name carrying only an
// absolute number has not yet said which season it belongs to.
func (p ParsedRelease) IsEpisode() bool { return len(p.Episodes) > 0 }

// IsAbsoluteEpisode reports whether the name named a series-wide episode number
// and nothing more — the anime shape. It is the complement of IsEpisode, never
// its overlap: such a file names an episode identity that still has to be
// placed against the series before anything can be filed.
func (p ParsedRelease) IsAbsoluteEpisode() bool {
	return len(p.Episodes) == 0 && p.Absolute > 0
}

// IsScene reports whether the parsed name describes a dated scene release —
// the adult module's release shape. A ParsedRelease is never both: Parse never
// sets SceneDate and Scene never sets Episodes.
func (p ParsedRelease) IsScene() bool { return !p.SceneDate.IsZero() }
