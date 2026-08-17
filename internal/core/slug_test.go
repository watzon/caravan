package core

import (
	"strings"
	"testing"
)

func TestLibrarySlug(t *testing.T) {
	cases := map[string]string{
		"Movies":                "movies",
		"Series":                "series",
		"Anime":                 "anime",
		"Kids Movies":           "kids-movies",
		"  The Kids  ":          "the-kids",
		"日本":                    "",
		"":                      "",
		strings.Repeat("a", 40): strings.Repeat("a", 32),
	}
	for name, want := range cases {
		got := LibrarySlug(name)
		if got != want {
			t.Errorf("LibrarySlug(%q) = %q, want %q", name, got, want)
			continue
		}
		if got == "" {
			continue
		}
		if !ValidLibrarySlug(got) {
			t.Errorf("LibrarySlug(%q) = %q, which ValidLibrarySlug refuses", name, got)
		}
	}
}

func TestValidLibrarySlug(t *testing.T) {
	ok := []string{"movies", "series", "a", "kids-2", "lib-4", "a1-b2"}
	for _, s := range ok {
		if !ValidLibrarySlug(s) {
			t.Errorf("ValidLibrarySlug(%q) = false, want true", s)
		}
	}
	bad := []string{"", "-movies", "Movies", "kids_movies", "has space", strings.Repeat("a", 33)}
	for _, s := range bad {
		if ValidLibrarySlug(s) {
			t.Errorf("ValidLibrarySlug(%q) = true, want false", s)
		}
	}
}
