package library

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// cleanRoot normalizes the storage root once, at construction, so every
// later join is predictable.
func cleanRoot(root string) string {
	if root == "" {
		return "."
	}
	return filepath.Clean(root)
}

// abs resolves a storage-root-relative path to an OS path. This and rel are
// the only two places the relative/absolute boundary is crossed.
//
// A path that is already absolute is returned as given. That is not a second
// path model: it is how the import pipeline reads a finished download an
// external client wrote outside the storage root, where the client's own
// directory is the only address the file has (PLAN phase 6, docs/external-
// clients.md). Such a path is never written to the database — see
// downloadPath, which is the only thing that produces one.
func (m *Manager) abs(rel string) string {
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel)
	}
	return filepath.Join(m.root, filepath.FromSlash(rel))
}

// downloadPath renders a file inside a finished download the way the rest of
// the import pipeline addresses files: storage-root-relative when the download
// landed under the root — the embedded engine, and an external client pointed
// inside it — and the client's own absolute path when it did not.
//
// foreignPath is the test for which one came back, and it gates every write:
// an absolute foreign path describes a file the library does not own and
// cannot address relative to its root, so it stays out of `media_files`,
// `unmatched_files` and `downloads.output_path` (SPEC §1.2 pillar 3).
func (m *Manager) downloadPath(abs string) string {
	r, err := filepath.Rel(m.root, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return filepath.Clean(abs)
	}
	return filepath.ToSlash(r)
}

// foreignPath reports whether p names a file outside Caravan's storage root.
func foreignPath(p string) bool { return filepath.IsAbs(p) }

// rel is abs' inverse: an OS path under the root becomes a slash-separated
// storage-root-relative path.
func (m *Manager) rel(abs string) (string, error) {
	r, err := filepath.Rel(m.root, abs)
	if err != nil {
		return "", fmt.Errorf("library: %s is not under storage root %s: %w", abs, m.root, err)
	}
	return filepath.ToSlash(r), nil
}

// illegalChars are the characters no path component may contain. The set is
// the union of the Windows-reserved characters and control characters, which
// also covers exFAT — the portable-mode filesystem (SPEC §3).
var illegalChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

// sanitize makes s safe to use as a single path component on every supported
// platform: illegal characters are dropped, runs of whitespace collapse, and
// trailing dots and spaces go (Windows silently strips them, which would make
// the name Caravan wrote differ from the name on disk).
func sanitize(s string) string {
	s = illegalChars.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(s), " ")
	s = strings.TrimRight(s, " .")
	if s == "" {
		return "Unknown"
	}
	return s
}

// titleYear renders the "Title (Year)" stem shared by legacy default names.
func titleYear(title string, year int) string {
	t := sanitize(title)
	if year <= 0 {
		return t
	}
	return fmt.Sprintf("%s (%d)", t, year)
}

const (
	defaultMovieFolderFormat  = "{title}{year}"
	defaultMovieFileFormat    = "{title}{year}{edition}"
	defaultSeriesFolderFormat = "{title}{year}"
	defaultSeasonFolderFormat = "Season {season:02}"
	defaultEpisodeFileFormat  = "{series}{year} - {episode}{title}"
)

type namingFormat struct {
	setting  string
	fallback string
	tokens   map[string]bool
	required []string
}

var namingFormats = []namingFormat{
	{store.SettingMovieFolderFormat, defaultMovieFolderFormat, map[string]bool{"title": true, "year": true}, []string{"title"}},
	{store.SettingMovieFileFormat, defaultMovieFileFormat, map[string]bool{"title": true, "year": true, "edition": true}, []string{"title"}},
	{store.SettingSeriesFolderFormat, defaultSeriesFolderFormat, map[string]bool{"title": true, "year": true}, []string{"title"}},
	{store.SettingSeasonFolderFormat, defaultSeasonFolderFormat, map[string]bool{"season": true, "season:02": true}, []string{"season"}},
	{store.SettingEpisodeFileFormat, defaultEpisodeFileFormat, map[string]bool{"series": true, "year": true, "episode": true, "title": true}, []string{"series", "episode"}},
}

func namingFormatFor(setting string) (namingFormat, bool) {
	for _, format := range namingFormats {
		if format.setting == setting {
			return format, true
		}
	}
	return namingFormat{}, false
}

// ValidateNamingSettings refuses a format before it reaches the settings
// table. It uses literal tokens only: formats are never Go templates or regex.
func ValidateNamingSettings(settings map[string]string) error {
	for key, value := range settings {
		format, ok := namingFormatFor(key)
		if !ok {
			continue
		}
		if err := validateNamingFormat(format, value); err != nil {
			return err
		}
	}
	return nil
}

func validateNamingFormat(format namingFormat, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", format.setting)
	}
	used := make(map[string]bool)
	for i := 0; i < len(value); {
		switch value[i] {
		case '{':
			end := strings.IndexByte(value[i:], '}')
			if end < 0 {
				return fmt.Errorf("%s has an unclosed token", format.setting)
			}
			token := value[i+1 : i+end]
			if !format.tokens[token] {
				return fmt.Errorf("%s has unknown token {%s}", format.setting, token)
			}
			used[token] = true
			i += end + 1
		case '}':
			return fmt.Errorf("%s has an unmatched closing brace", format.setting)
		default:
			if illegalChars.MatchString(string(value[i])) {
				return fmt.Errorf("%s contains an invalid path character", format.setting)
			}
			i++
		}
	}
	for _, token := range format.required {
		if token == "season" {
			if used["season"] || used["season:02"] {
				continue
			}
		} else if used[token] {
			continue
		}
		return fmt.Errorf("%s requires {%s}", format.setting, token)
	}
	return nil
}

func renderNamingFormat(format string, tokens map[string]string) string {
	for token, value := range tokens {
		format = strings.ReplaceAll(format, "{"+token+"}", value)
	}
	return format
}

func (m *Manager) format(setting, fallback string) string {
	value, err := m.store.GetSetting(context.Background(), setting)
	if err != nil || value == "" {
		return fallback
	}
	return value
}

// movieFolderName is the per-movie folder: "Big Buck Bunny (2008)".
func movieFolderName(title string, year int) string { return titleYear(title, year) }

func (m *Manager) movieFolderName(title string, year int) string {
	return renderNamingFormat(m.format(store.SettingMovieFolderFormat, defaultMovieFolderFormat), map[string]string{
		"title": sanitize(title),
		"year":  optionalYear(year),
	})
}

// movieFileName is the movie file: "Big Buck Bunny (2008).mkv".
func movieFileName(title string, year int, edition, ext string) string {
	name := titleYear(title, year)
	if e := sanitize(edition); edition != "" && e != "Unknown" {
		name += " - " + e
	}
	return name + ext
}

func (m *Manager) movieFileName(title string, year int, edition, ext string) string {
	return renderNamingFormat(m.format(store.SettingMovieFileFormat, defaultMovieFileFormat), map[string]string{
		"title":   sanitize(title),
		"year":    optionalYear(year),
		"edition": optionalEdition(edition),
	}) + ext
}

// seriesFolderName is the per-series folder: "Planet Earth II (2016)".
func seriesFolderName(title string, year int) string { return titleYear(title, year) }

func (m *Manager) seriesFolderName(title string, year int) string {
	return renderNamingFormat(m.format(store.SettingSeriesFolderFormat, defaultSeriesFolderFormat), map[string]string{
		"title": sanitize(title),
		"year":  optionalYear(year),
	})
}

func optionalYear(year int) string {
	if year <= 0 {
		return ""
	}
	return fmt.Sprintf(" (%d)", year)
}

func optionalEdition(edition string) string {
	if edition == "" {
		return ""
	}
	value := sanitize(edition)
	if value == "Unknown" {
		return ""
	}
	return " - " + value
}

// seasonFolderName is "Season 01".
func seasonFolderName(season int) string { return fmt.Sprintf("Season %02d", season) }

func (m *Manager) seasonFolderName(season int) string {
	return renderNamingFormat(m.format(store.SettingSeasonFolderFormat, defaultSeasonFolderFormat), map[string]string{
		"season":    fmt.Sprintf("%d", season),
		"season:02": fmt.Sprintf("%02d", season),
	})
}

// episodeFileName is "Planet Earth II (2016) - S01E01 - Islands.mkv".
func episodeFileName(title string, year, season int, episodes []int, episodeTitle, ext string) string {
	name := titleYear(title, year) + " - " + episodeTag(season, episodes)
	if t := sanitize(episodeTitle); episodeTitle != "" && t != "Unknown" {
		name += " - " + t
	}
	return name + ext
}

func (m *Manager) episodeFileName(title string, year, season int, episodes []int, episodeTitle, ext string) string {
	return renderNamingFormat(m.format(store.SettingEpisodeFileFormat, defaultEpisodeFileFormat), map[string]string{
		"series":  sanitize(title),
		"year":    optionalYear(year),
		"episode": episodeTag(season, episodes),
		"title":   optionalEpisodeTitle(episodeTitle),
	}) + ext
}

func optionalEpisodeTitle(title string) string {
	if title == "" {
		return ""
	}
	value := sanitize(title)
	if value == "Unknown" {
		return ""
	}
	return " - " + value
}

// sceneFileName is an adult episode's file:
// "Brazzers - 2022-03-14 - Deep Impact.mkv".
//
// The date rather than an SxxEyy tag, and that is load-bearing rather than
// cosmetic. A scene's episode number is Caravan's own mapping — the sequence
// within its release year — so the tag would be "S2022E01", which the release
// parser cannot read back (its season is one or two digits, as every real
// television season is). A rescan has to recognize the organizer's own output
// or the database stops being disposable, and the date is both what parse.Scene
// reads and what actually identifies the scene.
func sceneFileName(site string, date time.Time, sceneTitle, ext string) string {
	name := sanitize(site) + " - " + date.UTC().Format("2006-01-02")
	if t := sanitize(sceneTitle); sceneTitle != "" && t != "Unknown" {
		name += " - " + t
	}
	return name + ext
}

// episodeTag renders "S01E01" or "S01E01-E02" for a multi-episode file.
func episodeTag(season int, episodes []int) string {
	if len(episodes) == 0 {
		return fmt.Sprintf("S%02d", season)
	}
	tag := fmt.Sprintf("S%02dE%02d", season, episodes[0])
	for _, e := range episodes[1:] {
		tag += fmt.Sprintf("-E%02d", e)
	}
	return tag
}

// movieDir returns a movie folder's storage-root-relative path, under the
// root of the library that owns the movie. Which library that is was decided
// before any path is built — see libraries.go for the resolution rule.
func movieDir(lib *core.Library, title string, year int) string {
	return path.Join(lib.RootPath, movieFolderName(title, year))
}

// seriesDir returns a series folder's storage-root-relative path.
func seriesDir(lib *core.Library, title string, year int) string {
	return path.Join(lib.RootPath, seriesFolderName(title, year))
}

func (m *Manager) movieDir(lib *core.Library, title string, year int) string {
	return path.Join(lib.RootPath, m.movieFolderName(title, year))
}

func (m *Manager) seriesDir(lib *core.Library, title string, year int) string {
	return path.Join(lib.RootPath, m.seriesFolderName(title, year))
}

// sortTitle is the case-folded, article-stripped title the store orders by.
var leadingArticle = regexp.MustCompile(`^(?i)(the|a|an)\s+`)

func sortTitle(title string) string {
	return strings.ToLower(strings.TrimSpace(leadingArticle.ReplaceAllString(strings.TrimSpace(title), "")))
}
