package library

import (
	"encoding/xml"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// NFO filenames. Jellyfin and Kodi both read these names from the item folder
// (SPEC §5.1: NFO files are written alongside the media).
const (
	MovieNFOName  = "movie.nfo"
	TVShowNFOName = "tvshow.nfo"
)

// nfoDate is the date layout Kodi/Jellyfin expect in <premiered>.
const nfoDate = "2006-01-02"

// uniqueID is a Kodi/Jellyfin <uniqueid> element. Default marks the id a
// scraper should prefer.
type uniqueID struct {
	Type    string `xml:"type,attr"`
	Default bool   `xml:"default,attr,omitempty"`
	Value   string `xml:",chardata"`
}

type movieNFO struct {
	XMLName       xml.Name   `xml:"movie"`
	Title         string     `xml:"title"`
	OriginalTitle string     `xml:"originaltitle,omitempty"`
	SortTitle     string     `xml:"sorttitle,omitempty"`
	Year          int        `xml:"year,omitempty"`
	Plot          string     `xml:"plot,omitempty"`
	Premiered     string     `xml:"premiered,omitempty"`
	UniqueIDs     []uniqueID `xml:"uniqueid"`
}

type tvShowNFO struct {
	XMLName       xml.Name   `xml:"tvshow"`
	Title         string     `xml:"title"`
	OriginalTitle string     `xml:"originaltitle,omitempty"`
	SortTitle     string     `xml:"sorttitle,omitempty"`
	Year          int        `xml:"year,omitempty"`
	Plot          string     `xml:"plot,omitempty"`
	Premiered     string     `xml:"premiered,omitempty"`
	Status        string     `xml:"status,omitempty"`
	UniqueIDs     []uniqueID `xml:"uniqueid"`
}

// writeMovieNFO writes movie.nfo into the movie folder, which is
// storage-root-relative. It overwrites: the provider is authoritative for
// metadata, and a stale NFO is what makes a player show the wrong title.
func (m *Manager) writeMovieNFO(dir string, meta *core.MovieMeta) error {
	doc := movieNFO{
		Title:         meta.Title,
		OriginalTitle: meta.OriginalTitle,
		SortTitle:     sortTitle(meta.Title),
		Year:          meta.Year,
		Plot:          meta.Overview,
		Premiered:     formatNFODate(meta.ReleaseDate),
		UniqueIDs:     providerIDs(meta.TMDBID, 0, meta.IMDBID),
	}
	return m.writeXML(path.Join(dir, MovieNFOName), doc)
}

// writeTVShowNFO writes tvshow.nfo into the series folder.
func (m *Manager) writeTVShowNFO(dir string, meta *core.SeriesMeta) error {
	doc := tvShowNFO{
		Title:         meta.Title,
		OriginalTitle: meta.OriginalTitle,
		SortTitle:     sortTitle(meta.Title),
		Year:          meta.Year,
		Plot:          meta.Overview,
		Premiered:     formatNFODate(meta.FirstAirDate),
		Status:        meta.Status,
		UniqueIDs:     providerIDs(meta.TMDBID, meta.TVDBID, meta.IMDBID),
	}
	return m.writeXML(path.Join(dir, TVShowNFOName), doc)
}

// providerIDs renders the ids a scraper can key on, TMDB first because it is
// Caravan's primary provider (SPEC §4). Zero/empty ids are omitted.
func providerIDs(tmdbID, tvdbID int64, imdbID string) []uniqueID {
	ids := []uniqueID{}
	if tmdbID != 0 {
		ids = append(ids, uniqueID{Type: "tmdb", Default: true, Value: strconv.FormatInt(tmdbID, 10)})
	}
	if tvdbID != 0 {
		ids = append(ids, uniqueID{Type: "tvdb", Value: strconv.FormatInt(tvdbID, 10)})
	}
	if imdbID != "" {
		ids = append(ids, uniqueID{Type: "imdb", Value: imdbID})
	}
	return ids
}

func formatNFODate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(nfoDate)
}

// writeXML marshals doc to relPath, via a temporary file so a player never
// reads a half-written NFO.
func (m *Manager) writeXML(relPath string, doc any) error {
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("library: encode %s: %w", relPath, err)
	}
	out := append([]byte(xml.Header), body...)
	out = append(out, '\n')
	return m.writeFileAtomic(relPath, out)
}

// writeFileAtomic writes data to a storage-root-relative path through a
// temporary file in the same directory.
func (m *Manager) writeFileAtomic(relPath string, data []byte) error {
	absPath := m.abs(relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return fmt.Errorf("library: create %s: %w", path.Dir(relPath), err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(absPath), ".caravan-*")
	if err != nil {
		return fmt.Errorf("library: create temp beside %s: %w", relPath, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("library: write %s: %w", relPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("library: close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("library: chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, absPath); err != nil {
		return fmt.Errorf("library: rename %s to %s: %w", tmpName, relPath, err)
	}
	return nil
}
