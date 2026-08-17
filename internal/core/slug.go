package core

import "strings"

// Slugify reduces a display name to the shared slug alphabet: lowercase
// letters and digits, hyphens for everything else, runs of hyphens collapsed,
// ends trimmed, and the result capped at 32 characters.
//
// A name made entirely of characters the alphabet has no room for ("日本")
// yields "". Callers that must store a slug then pick a fallback rather than
// persist the empty string.
func Slugify(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			// Collapse on the way in, so the cap below counts real characters
			// rather than a run of separators.
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	slug := b.String()
	if len(slug) > 32 {
		slug = slug[:32]
	}
	// The cap can land mid-separator, and trailing dashes were legal until the
	// cut made them final.
	return strings.TrimRight(slug, "-")
}

// ValidSlug reports whether s is a stored slug: `^[a-z0-9][a-z0-9-]{0,31}$`.
// Empty is not valid — a row that needs a slug has to mint one.
func ValidSlug(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' && i > 0:
		default:
			return false
		}
	}
	return true
}

// LibrarySlug derives a URL slug from a library name. Same alphabet as
// provider instance slugs; an empty result means the name cannot become a
// path segment and the store must pick a fallback.
func LibrarySlug(name string) string {
	return Slugify(name)
}

// ValidLibrarySlug is ValidSlug under the name library call sites use.
func ValidLibrarySlug(s string) bool {
	return ValidSlug(s)
}
