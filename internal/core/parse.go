package core

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
	// Quality is one of the Quality* constants.
	Quality string
	// Source is one of the Source* constants.
	Source string
	// Codec is the video codec tag as parsed ("x265", "h264", …), or empty.
	Codec string
	// Audio is the audio tag as parsed ("DTS", "AAC", …), or empty.
	Audio string
	// Group is the release group, or empty.
	Group string
	// Proper marks a PROPER re-release.
	Proper bool
	// Repack marks a REPACK re-release.
	Repack bool
	// Edition is free-text movie edition ("Director's Cut", "Extended"), or
	// empty. SPEC §7 keeps this unstructured for v1.
	Edition string
	// Confidence is the parser's self-assessment in [0,1]. Low-confidence
	// results are what park files in the unmatched queue rather than being
	// imported silently.
	Confidence float64
}

// IsEpisode reports whether the parsed name describes a TV episode release.
func (p ParsedRelease) IsEpisode() bool { return len(p.Episodes) > 0 }
