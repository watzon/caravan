package nzb

import (
	"regexp"
	"strings"
)

// yencCounter matches the trailing "yEnc (1/50)" (or bare "yEnc") that almost
// every binary subject ends with. Stripping it is what leaves a filename at
// the end of an unquoted subject.
var yencCounter = regexp.MustCompile(`(?i)\s+yenc\b(\s*\(\d+/\d+\))?\s*$`)

// leadingCounter matches the "[01/12] - " or "(01/12) - " prefix posters put
// in front of a filename.
var leadingCounter = regexp.MustCompile(`^\s*[\[(]\s*\d+\s*/\s*\d+\s*[\])]\s*-?\s*`)

// Filename recovers a filename from an article subject.
//
// The rules, in order:
//
//  1. A quoted run wins. "…/[01/12] - "name.part01.rar" yEnc (1/50)" is the
//     conventional shape and the quotes are unambiguous.
//  2. Otherwise the yEnc counter and any leading part counter are stripped,
//     and the last whitespace-separated token that looks like a filename
//     (it has a dot with something after it) is taken.
//  3. Otherwise the trimmed subject itself, because a wrong-looking name is
//     more useful downstream than an empty one: an obfuscated release is
//     the stuck-import queue's problem (SPEC §5.4), not the parser's.
//
// Filename never returns a path: any directory separators a poster smuggled
// into the subject are dropped, since the result is joined onto the download
// directory.
func Filename(subject string) string {
	s := strings.TrimSpace(subject)
	if s == "" {
		return ""
	}

	if name, ok := quoted(s); ok {
		return sanitize(name)
	}

	s = yencCounter.ReplaceAllString(s, "")
	s = leadingCounter.ReplaceAllString(s, "")
	s = strings.TrimSpace(strings.Trim(strings.TrimSpace(s), "-"))
	if s == "" {
		return sanitize(strings.TrimSpace(subject))
	}

	fields := strings.Fields(s)
	for i := len(fields) - 1; i >= 0; i-- {
		if looksLikeFilename(fields[i]) {
			return sanitize(fields[i])
		}
	}
	return sanitize(s)
}

// quoted returns the contents of the first double-quoted run in s.
func quoted(s string) (string, bool) {
	start := strings.IndexByte(s, '"')
	if start < 0 {
		return "", false
	}
	rest := s[start+1:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return "", false
	}
	name := strings.TrimSpace(rest[:end])
	return name, name != ""
}

// looksLikeFilename reports whether a token carries an extension.
func looksLikeFilename(tok string) bool {
	dot := strings.LastIndexByte(tok, '.')
	return dot > 0 && dot < len(tok)-1
}

// sanitize strips path separators and control characters, so a filename lifted
// out of an untrusted subject can never climb out of the download directory.
func sanitize(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\':
			return -1
		case r < ' ' || r == 0x7F:
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	// "." and ".." are directory references, never filenames.
	if name == "." || name == ".." {
		return ""
	}
	return name
}
