// Package htmltext turns the prose a metadata provider serves as HTML into the
// plain text Caravan stores and writes out.
//
// Providers put markup in their descriptions whether or not their API says so.
// AniList emits <br> between paragraphs and <i> around a Japanese phrase even
// when asked for asHtml:false; TVmaze's `summary` is genuine HTML by contract.
// Those strings are written verbatim into tvshow.nfo, so leaving the markup in
// would put unescaped-looking tags in an XML document and show them literally
// in every player that reads it. Block tags become newlines so the paragraphs
// survive; everything else is dropped.
//
// This lives in its own package rather than in one provider because it is now
// on the third caller, and three private copies of a text transform drift: a
// fix made where the bug was noticed does not reach the NFOs written from the
// other two.
package htmltext

import (
	"html"
	"strings"
)

// Strip turns a provider description into plain text.
//
// Unescaping happens LAST, after the tags are gone. That order is the whole
// difference between prose and markup: an entity-escaped "&lt;i&gt;" written by
// an author who meant to talk about the tag stays text, instead of being
// re-read as a tag and swallowed.
func Strip(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '<' {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			// An unterminated '<' is a literal less-than in the prose, not a
			// tag: keep it rather than swallow the rest of the description.
			b.WriteString(s[i:])
			break
		}
		if isBlockTag(s[i+1 : i+end]) {
			b.WriteByte('\n')
		}
		i += end + 1
	}
	return tidyLines(html.UnescapeString(b.String()))
}

// isBlockTag reports whether a tag's contents end a line of prose.
func isBlockTag(tag string) bool {
	name := strings.TrimPrefix(strings.TrimSpace(tag), "/")
	if i := strings.IndexAny(name, " \t/"); i >= 0 {
		name = name[:i]
	}
	switch strings.ToLower(name) {
	case "br", "p", "div":
		return true
	}
	return false
}

// tidyLines trims each line and collapses the runs of blank lines that stripping
// <br><br></p> leaves behind, so the paragraph breaks survive and nothing else
// does.
func tidyLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" && (len(out) == 0 || out[len(out)-1] == "") {
			continue
		}
		out = append(out, line)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}
