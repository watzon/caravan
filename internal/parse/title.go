package parse

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// stripDiacritics folds "Amélie" to "Amelie": release names usually drop
// accents that metadata titles keep.
var stripDiacritics = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// TitleSlug reduces a title to the form two spellings of the same work share:
// lowercase letters and digits of any script, accents folded, dotted acronyms
// flattened, "&" spelled out, apostrophes dropped, everything else a word
// break, and one leading article removed. "The Matrix" and "Matrix" agree;
// "Marvel's Agents of S.H.I.E.L.D." and "Marvels Agents of SHIELD" agree.
func TitleSlug(title string) string {
	title = reAcronym.ReplaceAllStringFunc(title, func(m string) string {
		return strings.ReplaceAll(m, ".", "")
	})
	if folded, _, err := transform.String(stripDiacritics, title); err == nil {
		title = folded
	}
	title = strings.ToLower(title)
	title = strings.ReplaceAll(title, "&", " and ")
	title = strings.NewReplacer("'", "", "’", "").Replace(title)
	var b strings.Builder
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteByte(' ')
	}
	fields := strings.Fields(b.String())
	if len(fields) > 1 {
		switch fields[0] {
		case "the", "a", "an":
			fields = fields[1:]
		}
	}
	return strings.Join(fields, " ")
}

// SameTitle reports whether a parsed release title names the requested work.
// It is a display heuristic for flagging, not a rejection gate: alternate
// titles a provider knows but Caravan does not store will read as different.
// Either side reducing to nothing is "cannot tell", never a mismatch.
func SameTitle(requested, parsed string) bool {
	a, b := TitleSlug(requested), TitleSlug(parsed)
	if a == "" || b == "" {
		return true
	}
	return a == b
}
