package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

const calendarDateFormat = "2006-01-02"

type calendarResponse struct {
	Entries []calendarEntry `json:"entries"`
}

// calendarEntry is deliberately one shape for both kinds so clients can sort
// and render a single timeline. Fields that do not apply to a movie are absent
// rather than overloaded with zero values.
type calendarEntry struct {
	ID            int64  `json:"-"`
	Year          int    `json:"-"`
	Kind          string `json:"kind"`
	Date          string `json:"date"`
	Title         string `json:"title"`
	SeriesID      int64  `json:"series_id,omitempty"`
	MovieID       int64  `json:"movie_id,omitempty"`
	SeasonNumber  int    `json:"season_number,omitempty"`
	EpisodeNumber int    `json:"episode_number,omitempty"`
	EpisodeTitle  string `json:"episode_title,omitempty"`
	Monitored     bool   `json:"monitored"`
	HasFile       bool   `json:"has_file"`
	Status        string `json:"status"`
}

// handleCalendar is the combined movie and episode calendar (PLAN phase 3,
// task 9). It keeps the date range at the HTTP boundary while the store owns
// the joined file-state queries needed to build its entries.
func (s *server) handleCalendar(w http.ResponseWriter, r *http.Request) {
	today := calendarDate(time.Now())
	start, end, ok := calendarRange(w, r, today, 7, 90)
	if !ok {
		return
	}

	entries, err := s.calendarEntries(r.Context(), start, end, today)
	if err != nil {
		s.writeStoreError(w, "list calendar entries", err)
		return
	}
	writeJSON(w, http.StatusOK, calendarResponse{Entries: entries})
}

// handleCalendarICS is the API-key-protected iCal feed for external calendar
// apps. Its wider fixed range avoids making a subscriber negotiate UI filters.
//
// It carries its own check because it is exempt from the password gate: a
// calendar app subscribes to a URL and cannot hold a session cookie, so the API
// key is the only credential it can present (SPEC §11).
func (s *server) handleCalendarICS(w http.ResponseWriter, r *http.Request) {
	authorized, err := s.calendarKeyAuthenticated(r)
	if err != nil {
		s.writeStoreError(w, "read api key", err)
		return
	}
	if !authorized {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	today := calendarDate(time.Now())
	entries, err := s.calendarEntries(r.Context(), today.AddDate(0, 0, -30), today.AddDate(0, 0, 365), today)
	if err != nil {
		s.writeStoreError(w, "list calendar entries", err)
		return
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	_, _ = io.WriteString(w, calendarICS(entries))
}

// handleGenerateAPIKey (re)generates the API key the iCal feed authenticates
// with (PLAN phase 3, task 9). Replacing rather than extending the old key
// makes regeneration an immediate revocation.
func (s *server) handleGenerateAPIKey(w http.ResponseWriter, r *http.Request) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		s.log.Error("generate api key", "error", err)
		writeError(w, http.StatusInternalServerError, "generate api key")
		return
	}
	apiKey := hex.EncodeToString(raw[:])
	if err := s.st.SetSetting(r.Context(), store.SettingAPIKey, apiKey); err != nil {
		s.writeStoreError(w, "write api key", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"api_key": apiKey})
}

// calendarRange accepts date-only bounds so every client sees the same all-day
// entries regardless of its local time zone. Missing bounds use a practical UI
// window around today; callers that need a wider feed use calendarEntries.
func calendarRange(w http.ResponseWriter, r *http.Request, today time.Time, daysBack, daysAhead int) (time.Time, time.Time, bool) {
	start := today.AddDate(0, 0, -daysBack)
	end := today.AddDate(0, 0, daysAhead)
	params := r.URL.Query()
	if values, supplied := params["start"]; supplied {
		if len(values) != 1 {
			writeError(w, http.StatusBadRequest, "start must be YYYY-MM-DD")
			return time.Time{}, time.Time{}, false
		}
		var ok bool
		start, ok = parseCalendarDate(values[0])
		if !ok {
			writeError(w, http.StatusBadRequest, "start must be YYYY-MM-DD")
			return time.Time{}, time.Time{}, false
		}
	}
	if values, supplied := params["end"]; supplied {
		if len(values) != 1 {
			writeError(w, http.StatusBadRequest, "end must be YYYY-MM-DD")
			return time.Time{}, time.Time{}, false
		}
		var ok bool
		end, ok = parseCalendarDate(values[0])
		if !ok {
			writeError(w, http.StatusBadRequest, "end must be YYYY-MM-DD")
			return time.Time{}, time.Time{}, false
		}
	}
	if end.Before(start) {
		writeError(w, http.StatusBadRequest, "end must not be before start")
		return time.Time{}, time.Time{}, false
	}
	return start, end, true
}

func parseCalendarDate(raw string) (time.Time, bool) {
	date, err := time.Parse(calendarDateFormat, raw)
	return date, err == nil && date.Format(calendarDateFormat) == raw
}

func calendarDate(t time.Time) time.Time {
	year, month, day := t.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// calendarEntries merges rows before assigning status so a grab can cover a
// season pack and a single download lookup remains enough for every entry.
func (s *server) calendarEntries(ctx context.Context, start, end, today time.Time) ([]calendarEntry, error) {
	episodes, err := s.st.CalendarEpisodes(ctx, start, end)
	if err != nil {
		return nil, err
	}
	movies, err := s.st.CalendarMovies(ctx, start, end)
	if err != nil {
		return nil, err
	}

	downloadingMovies, downloadingEpisodes, err := s.downloadingCalendarItems(ctx)
	if err != nil {
		return nil, err
	}

	entries := make([]calendarEntry, 0, len(episodes)+len(movies))
	for _, episode := range episodes {
		entries = append(entries, calendarEntry{
			ID:            episode.Episode.ID,
			Kind:          "episode",
			Date:          episode.Episode.AirDate.Format(calendarDateFormat),
			Title:         episode.SeriesTitle,
			SeriesID:      episode.Episode.SeriesID,
			SeasonNumber:  episode.Episode.SeasonNumber,
			EpisodeNumber: episode.Episode.EpisodeNumber,
			EpisodeTitle:  episode.Episode.Title,
			Monitored:     episode.Episode.Monitored,
			HasFile:       episode.HasFile,
			Status: calendarStatus(episode.HasFile, downloadingEpisodes[episode.Episode.ID],
				episode.Episode.AirDate, today),
		})
	}
	for _, movie := range movies {
		entries = append(entries, calendarEntry{
			ID:        movie.Movie.ID,
			Year:      movie.Movie.Year,
			Kind:      "movie",
			Date:      movie.Movie.ReleaseDate.Format(calendarDateFormat),
			Title:     movie.Movie.Title,
			MovieID:   movie.Movie.ID,
			Monitored: movie.Movie.Monitored,
			HasFile:   movie.HasFile,
			Status:    calendarStatus(movie.HasFile, downloadingMovies[movie.Movie.ID], movie.Movie.ReleaseDate, today),
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Date != entries[j].Date {
			return entries[i].Date < entries[j].Date
		}
		if entries[i].Title != entries[j].Title {
			return entries[i].Title < entries[j].Title
		}
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		if entries[i].SeasonNumber != entries[j].SeasonNumber {
			return entries[i].SeasonNumber < entries[j].SeasonNumber
		}
		return entries[i].EpisodeNumber < entries[j].EpisodeNumber
	})
	return entries, nil
}

func calendarStatus(hasFile, downloading bool, date, today time.Time) string {
	switch {
	case hasFile:
		return "downloaded"
	case downloading:
		return "downloading"
	case calendarDate(date).After(today):
		return "unaired"
	default:
		return "missing"
	}
}

// downloadingCalendarItems translates durable grab and download history into
// the small status predicate the calendar needs. A freshly inserted grab has
// no download row briefly, and must still render as downloading.
func (s *server) downloadingCalendarItems(ctx context.Context) (map[int64]bool, map[int64]bool, error) {
	grabs, err := s.st.ListGrabs(ctx, 0)
	if err != nil {
		return nil, nil, err
	}
	downloads, err := s.st.ListDownloads(ctx)
	if err != nil {
		return nil, nil, err
	}

	type downloadState struct {
		hasAny bool
		active bool
	}
	byGrabID := make(map[int64]downloadState, len(downloads))
	for _, download := range downloads {
		if download.GrabID == 0 {
			continue
		}
		state := byGrabID[download.GrabID]
		state.hasAny = true
		state.active = state.active || download.State != core.DownloadFailed
		byGrabID[download.GrabID] = state
	}

	movies := make(map[int64]bool)
	episodes := make(map[int64]bool)
	for _, grab := range grabs {
		if grab.Status != core.GrabStatusGrabbed {
			continue
		}
		state := byGrabID[grab.GrabID]
		if state.hasAny && !state.active {
			continue
		}
		if grab.MovieID != 0 {
			movies[grab.MovieID] = true
		}
		for _, episodeID := range grab.EpisodeIDs {
			episodes[episodeID] = true
		}
	}
	return movies, episodes, nil
}

func calendarICS(entries []calendarEntry) string {
	var out strings.Builder
	out.WriteString("BEGIN:VCALENDAR\r\n")
	out.WriteString("VERSION:2.0\r\n")
	out.WriteString("PRODID:-//Caravan//EN\r\n")
	out.WriteString("CALSCALE:GREGORIAN\r\n")
	for _, entry := range entries {
		out.WriteString("BEGIN:VEVENT\r\n")
		fmt.Fprintf(&out, "UID:%s\r\n", calendarUID(entry))
		fmt.Fprintf(&out, "DTSTART;VALUE=DATE:%s\r\n", strings.ReplaceAll(entry.Date, "-", ""))
		fmt.Fprintf(&out, "SUMMARY:%s\r\n", escapeICalText(calendarSummary(entry)))
		fmt.Fprintf(&out, "DESCRIPTION:%s\r\n", escapeICalText("Status: "+entry.Status))
		out.WriteString("END:VEVENT\r\n")
	}
	out.WriteString("END:VCALENDAR\r\n")
	return out.String()
}

func calendarUID(entry calendarEntry) string {
	if entry.Kind == "episode" {
		return fmt.Sprintf("episode-%d-%s@caravan", entry.ID, entry.Date)
	}
	return fmt.Sprintf("movie-%d-%s@caravan", entry.ID, entry.Date)
}

func calendarSummary(entry calendarEntry) string {
	if entry.Kind == "episode" {
		return fmt.Sprintf("%s S%02dE%02d - %s", entry.Title, entry.SeasonNumber, entry.EpisodeNumber, entry.EpisodeTitle)
	}
	if entry.Year == 0 {
		return entry.Title
	}
	return fmt.Sprintf("%s (%d)", entry.Title, entry.Year)
}

func escapeICalText(value string) string {
	return strings.NewReplacer(
		"\\", "\\\\",
		"\r\n", "\\n",
		"\n", "\\n",
		"\r", "\\n",
		";", "\\;",
		",", "\\,",
	).Replace(value)
}
