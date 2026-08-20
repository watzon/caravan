package api

import (
	"context"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

func TestCalendarMergesEntriesAndAssignsStatuses(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	today := calendarDate(time.Now())

	series := &core.Series{
		TMDBID: 100, Title: "Calendar Show", SortTitle: "calendar show", Monitored: true,
		LibraryID: defaultLibraryID(t, st, core.LibraryKindTV),
	}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	downloaded := storeCalendarEpisode(t, st, series.ID, 1, 1, "Downloaded", today.AddDate(0, 0, -5), false)
	storeCalendarEpisode(t, st, series.ID, 1, 2, "Missing", today.AddDate(0, 0, -4), true)
	downloading := storeCalendarEpisode(t, st, series.ID, 1, 3, "Downloading", today.AddDate(0, 0, -3), true)
	storeCalendarEpisode(t, st, series.ID, 1, 4, "Future", today.AddDate(0, 0, 4), true)
	storeCalendarEpisode(t, st, series.ID, 1, 5, "Excluded", today.AddDate(0, 0, -2), false)

	file := &core.MediaFile{Path: "TV/Calendar Show/Season 01/S01E01.mkv", Size: 1}
	if err := st.UpsertMediaFile(ctx, file); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	if err := st.LinkEpisodeFile(ctx, downloaded.ID, file.ID); err != nil {
		t.Fatalf("LinkEpisodeFile: %v", err)
	}

	grabbedEpisode := &core.Grab{GrabInfo: core.GrabInfo{SeriesID: series.ID, EpisodeIDs: []int64{downloading.ID}}}
	if err := st.InsertGrab(ctx, grabbedEpisode); err != nil {
		t.Fatalf("InsertGrab episode: %v", err)
	}

	movieDownloading := storeCalendarMovie(t, st, "Movie Downloading", 2020, today.AddDate(0, 0, -2), false)
	movieFailed := storeCalendarMovie(t, st, "Movie Failed", 2021, today.AddDate(0, 0, -1), true)
	movieFuture := storeCalendarMovie(t, st, "Movie Future", 2022, today.AddDate(0, 0, 2), false)
	grabbedMovie := &core.Grab{GrabInfo: core.GrabInfo{MovieID: movieDownloading.ID}}
	if err := st.InsertGrab(ctx, grabbedMovie); err != nil {
		t.Fatalf("InsertGrab movie: %v", err)
	}
	if err := st.UpsertDownload(ctx, &core.Download{GrabID: grabbedMovie.GrabID, Engine: "test", EngineID: "movie-downloading", State: core.DownloadQueued}); err != nil {
		t.Fatalf("UpsertDownload active: %v", err)
	}
	failedGrab := &core.Grab{GrabInfo: core.GrabInfo{MovieID: movieFailed.ID}}
	if err := st.InsertGrab(ctx, failedGrab); err != nil {
		t.Fatalf("InsertGrab failed: %v", err)
	}
	if err := st.UpsertDownload(ctx, &core.Download{GrabID: failedGrab.GrabID, Engine: "test", EngineID: "movie-failed", State: core.DownloadFailed}); err != nil {
		t.Fatalf("UpsertDownload failed: %v", err)
	}

	unrelatedMovie := storeCalendarMovie(t, st, "Unrelated History", 2023, today.AddDate(0, 0, -30), true)
	unrelatedGrab := &core.Grab{GrabInfo: core.GrabInfo{MovieID: unrelatedMovie.ID}}
	if err := st.InsertGrab(ctx, unrelatedGrab); err != nil {
		t.Fatalf("InsertGrab unrelated: %v", err)
	}
	if err := st.UpsertDownload(ctx, &core.Download{
		GrabID: unrelatedGrab.GrabID, Engine: "test", EngineID: "unrelated-active", State: core.DownloadQueued,
	}); err != nil {
		t.Fatalf("UpsertDownload unrelated: %v", err)
	}

	start := today.AddDate(0, 0, -7).Format(calendarDateFormat)
	end := today.AddDate(0, 0, 7).Format(calendarDateFormat)
	rec := do(t, h, http.MethodGet, "/api/v1/calendar?start="+start+"&end="+end, "")
	wantStatus(t, rec, http.StatusOK)
	var body calendarResponse
	decodeBody(t, rec, &body)

	if got, want := len(body.Entries), 7; got != want {
		t.Fatalf("entries = %d, want %d: %+v", got, want, body.Entries)
	}
	assertCalendarEntry(t, body.Entries, "episode", "Downloaded", "downloaded", true)
	assertCalendarEntry(t, body.Entries, "episode", "Missing", "missing", false)
	assertCalendarEntry(t, body.Entries, "episode", "Downloading", "downloading", false)
	assertCalendarEntry(t, body.Entries, "episode", "Future", "unaired", false)
	assertCalendarEntry(t, body.Entries, "movie", "Movie Downloading", "downloading", false)
	assertCalendarEntry(t, body.Entries, "movie", "Movie Failed", "missing", false)
	assertCalendarEntry(t, body.Entries, "movie", "Movie Future", "unaired", false)

	tvLib := defaultLibraryID(t, st, core.LibraryKindTV)
	movieLib := defaultLibraryID(t, st, core.LibraryKindMovie)

	entry := findCalendarEntry(t, body.Entries, "episode", "Downloaded")
	if entry.SeriesID != series.ID || entry.MovieID != 0 || entry.EpisodeID != downloaded.ID ||
		entry.SeriesKind != core.SeriesKindTV || entry.SeasonNumber != 1 || entry.EpisodeNumber != 1 ||
		entry.Title != "Calendar Show" || entry.EpisodeTitle != "Downloaded" || entry.Date != downloaded.AirDate.Format(calendarDateFormat) ||
		entry.LibraryID != tvLib {
		t.Fatalf("episode identity = %+v, want series and episode identifiers", entry)
	}
	entry = findCalendarEntry(t, body.Entries, "movie", "Movie Future")
	if entry.MovieID != movieFuture.ID || entry.SeriesID != 0 || entry.EpisodeTitle != "" ||
		entry.LibraryID != movieLib {
		t.Fatalf("movie identity = %+v, want movie identifier only", entry)
	}
}

func TestCalendarFiltersAndDefaults(t *testing.T) {
	h, st, _ := newTestServer(t)
	today := calendarDate(time.Now())
	before := storeCalendarMovie(t, st, "Before Default", 2020, today.AddDate(0, 0, -8), false)
	inside := storeCalendarMovie(t, st, "Inside Default", 2020, today.AddDate(0, 0, 10), false)
	storeCalendarMovie(t, st, "After Default", 2020, today.AddDate(0, 0, 91), false)

	rec := do(t, h, http.MethodGet, "/api/v1/calendar", "")
	wantStatus(t, rec, http.StatusOK)
	var body calendarResponse
	decodeBody(t, rec, &body)
	if got, want := len(body.Entries), 1; got != want || body.Entries[0].MovieID != inside.ID {
		t.Fatalf("default entries = %+v, want only movie %d", body.Entries, inside.ID)
	}

	date := before.ReleaseDate.Format(calendarDateFormat)
	rec = do(t, h, http.MethodGet, "/api/v1/calendar?start="+date+"&end="+date, "")
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &body)
	if got, want := len(body.Entries), 1; got != want || body.Entries[0].MovieID != before.ID {
		t.Fatalf("filtered entries = %+v, want only movie %d", body.Entries, before.ID)
	}
}

func TestCalendarRejectsInvalidDates(t *testing.T) {
	h, _, _ := newTestServer(t)

	for _, target := range []string{
		"/api/v1/calendar?start=not-a-date",
		"/api/v1/calendar?start=",
		"/api/v1/calendar?end=2026-02-30",
		"/api/v1/calendar?start=2026-01-02&end=2026-01-01",
	} {
		t.Run(target, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, target, "")
			wantStatus(t, rec, http.StatusBadRequest)
			wantErrorBody(t, rec)
		})
	}
}

func TestCalendarICalRequiresKeyAndServesEvents(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	today := calendarDate(time.Now())

	series := &core.Series{TMDBID: 101, Title: "ICS Show", SortTitle: "ics show"}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	episode := storeCalendarEpisode(t, st, series.ID, 1, 2, "Pilot", today.AddDate(0, 0, 3), true)
	movie := storeCalendarMovie(t, st, "ICS Movie", 2020, today.AddDate(0, 0, -2), true)
	movieFile := &core.MediaFile{Path: "Movies/ICS Movie (2020)/ICS Movie.mkv", MovieID: movie.ID}
	if err := st.UpsertMediaFile(ctx, movieFile); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingAPIKey, "correct-key"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	for _, target := range []string{"/api/v1/calendar.ics", "/api/v1/calendar.ics?apikey=wrong-key"} {
		t.Run(target, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, target, "")
			wantStatus(t, rec, http.StatusUnauthorized)
			wantErrorBody(t, rec)
		})
	}

	rec := do(t, h, http.MethodGet, "/api/v1/calendar.ics?apikey=correct-key", "")
	wantStatus(t, rec, http.StatusOK)
	if got, want := rec.Header().Get("Content-Type"), "text/calendar; charset=utf-8"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	events := parseCalendarEvents(t, rec.Body.String())
	if got, want := len(events), 2; got != want {
		t.Fatalf("events = %d, want %d: %q", got, want, rec.Body.String())
	}
	wantICalEvent(t, events, "episode-"+itoa(episode.ID)+"-"+episode.AirDate.Format(calendarDateFormat)+"@caravan", episode.AirDate, "ICS Show S01E02 - Pilot", "Status: unaired")
	wantICalEvent(t, events, "movie-"+itoa(movie.ID)+"-"+movie.ReleaseDate.Format(calendarDateFormat)+"@caravan", movie.ReleaseDate, "ICS Movie (2020)", "Status: downloaded")
}

func TestGenerateAPIKeyRegeneratesAndRevokesOldKey(t *testing.T) {
	h, st, _ := newTestServer(t)

	first := generateCalendarAPIKey(t, h)
	if stored, err := st.GetSetting(context.Background(), store.SettingAPIKey); err != nil || stored != first {
		t.Fatalf("stored api key = %q, %v, want generated key", stored, err)
	}
	wantStatus(t, do(t, h, http.MethodGet, "/api/v1/calendar.ics?apikey="+first, ""), http.StatusOK)

	second := generateCalendarAPIKey(t, h)
	if first == second {
		t.Fatal("regenerated api key did not change")
	}
	wantStatus(t, do(t, h, http.MethodGet, "/api/v1/calendar.ics?apikey="+first, ""), http.StatusUnauthorized)
	wantStatus(t, do(t, h, http.MethodGet, "/api/v1/calendar.ics?apikey="+second, ""), http.StatusOK)
}

func storeCalendarEpisode(t *testing.T, st *store.Store, seriesID int64, season, number int, title string, date time.Time, monitored bool) *core.Episode {
	t.Helper()
	episode := &core.Episode{SeriesID: seriesID, SeasonNumber: season, EpisodeNumber: number, Title: title, AirDate: date, Monitored: monitored}
	if err := st.UpsertEpisode(context.Background(), episode); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	return episode
}

func storeCalendarMovie(t *testing.T, st *store.Store, title string, year int, date time.Time, monitored bool) *core.Movie {
	t.Helper()
	movie := &core.Movie{
		Title: title, SortTitle: strings.ToLower(title), Year: year, ReleaseDate: date, Monitored: monitored,
		LibraryID: defaultLibraryID(t, st, core.LibraryKindMovie),
	}
	if err := st.UpsertMovie(context.Background(), movie); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	return movie
}

func assertCalendarEntry(t *testing.T, entries []calendarEntry, kind, name, status string, hasFile bool) {
	t.Helper()
	entry := findCalendarEntry(t, entries, kind, name)
	if entry.Status != status || entry.HasFile != hasFile {
		t.Fatalf("%s %q = %+v, want status %q and has_file %t", kind, name, entry, status, hasFile)
	}
}

func findCalendarEntry(t *testing.T, entries []calendarEntry, kind, name string) calendarEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Kind != kind {
			continue
		}
		if (kind == "episode" && entry.EpisodeTitle == name) || (kind == "movie" && entry.Title == name) {
			return entry
		}
	}
	t.Fatalf("%s entry %q not found in %+v", kind, name, entries)
	return calendarEntry{}
}

func generateCalendarAPIKey(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/api/v1/settings/apikey", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		APIKey string `json:"api_key"`
	}
	decodeBody(t, rec, &body)
	if len(body.APIKey) != 32 {
		t.Fatalf("api key length = %d, want 32", len(body.APIKey))
	}
	if _, err := hex.DecodeString(body.APIKey); err != nil {
		t.Fatalf("api key %q is not hexadecimal: %v", body.APIKey, err)
	}
	return body.APIKey
}

func parseCalendarEvents(t *testing.T, body string) map[string]map[string]string {
	t.Helper()
	if !strings.HasPrefix(body, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Caravan//EN\r\n") || !strings.HasSuffix(body, "END:VCALENDAR\r\n") {
		t.Fatalf("invalid VCALENDAR envelope: %q", body)
	}

	events := make(map[string]map[string]string)
	var event map[string]string
	for _, line := range strings.Split(body, "\r\n") {
		switch line {
		case "":
			continue
		case "BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Caravan//EN", "CALSCALE:GREGORIAN", "END:VCALENDAR":
			continue
		case "BEGIN:VEVENT":
			if event != nil {
				t.Fatal("nested VEVENT")
			}
			event = make(map[string]string)
		case "END:VEVENT":
			if event == nil || event["UID"] == "" {
				t.Fatalf("VEVENT without UID: %q", body)
			}
			events[event["UID"]] = event
			event = nil
		default:
			if event == nil {
				t.Fatalf("property outside VEVENT: %q", line)
			}
			key, value, found := strings.Cut(line, ":")
			if !found {
				t.Fatalf("invalid iCal property: %q", line)
			}
			event[key] = value
		}
	}
	if event != nil {
		t.Fatal("unterminated VEVENT")
	}
	return events
}

func wantICalEvent(t *testing.T, events map[string]map[string]string, uid string, date time.Time, summary, description string) {
	t.Helper()
	event, ok := events[uid]
	if !ok {
		t.Fatalf("event %q not found in %+v", uid, events)
	}
	if got, want := event["DTSTART;VALUE=DATE"], date.Format("20060102"); got != want {
		t.Fatalf("event %q DTSTART = %q, want %q", uid, got, want)
	}
	if got := event["SUMMARY"]; got != summary {
		t.Fatalf("event %q SUMMARY = %q, want %q", uid, got, summary)
	}
	if got := event["DESCRIPTION"]; got != description {
		t.Fatalf("event %q DESCRIPTION = %q, want %q", uid, got, description)
	}
}
