package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/convert"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// stubConverter stands in for internal/convert. The HTTP layer asks it for
// availability and optional live progress for running jobs.
type stubConverter struct {
	available bool
	progress  map[int64]convert.LiveProgress
}

func (c stubConverter) Available() bool { return c.available }

func (c stubConverter) Progress(id int64) (convert.LiveProgress, bool) {
	progress, ok := c.progress[id]
	return progress, ok
}

func seedConvertibleFile(t *testing.T, st *store.Store, path string) *core.MediaFile {
	t.Helper()
	movie := &core.Movie{TMDBID: 1, Title: "Movie", SortTitle: "movie"}
	if err := st.UpsertMovie(context.Background(), movie); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	f := core.MediaFile{
		Path: path, Size: 1234, MovieID: movie.ID,
		Codec: "x265", Audio: "DTS", Quality: core.Quality2160p,
		AddedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := st.UpsertMediaFile(context.Background(), &f); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	return &f
}

func TestConvertQueueRoundTrip(t *testing.T) {
	h, st, _ := newTestServer(t, WithConverter(stubConverter{available: true}))
	file := seedConvertibleFile(t, st, "library/Movies/A (2001)/A (2001).mkv")
	remux := core.MediaFile{
		Path: "library/Movies/B (2002)/B (2002).mkv", Size: 2345, MovieID: file.MovieID,
		Codec: "x264", Audio: "AAC", Quality: core.Quality1080p,
		AddedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	compatible := core.MediaFile{
		Path: "library/Movies/C (2003)/C (2003).mp4", Size: 3456, MovieID: file.MovieID,
		Codec: "x264", Audio: "AAC", Quality: core.Quality1080p,
	}
	for _, candidate := range []*core.MediaFile{&remux, &compatible} {
		if err := st.UpsertMediaFile(context.Background(), candidate); err != nil {
			t.Fatalf("UpsertMediaFile(%q): %v", candidate.Path, err)
		}
	}

	// The page starts with every current file that needs work, newest added
	// first, but not a file that already matches the active profile.
	rec := do(t, h, "GET", "/api/v1/convert", "")
	wantStatus(t, rec, http.StatusOK)
	var empty struct {
		Pending     []mediaFileJSON  `json:"pending"`
		Conversions []conversionJSON `json:"conversions"`
	}
	decodeBody(t, rec, &empty)
	if empty.Conversions == nil || len(empty.Conversions) != 0 {
		t.Fatalf("conversions = %v, want an empty list", empty.Conversions)
	}
	if len(empty.Pending) != 2 {
		t.Fatalf("pending = %+v, want incompatible and remux files", empty.Pending)
	}
	if empty.Pending[0].ID != remux.ID ||
		empty.Pending[0].Compatibility.Verdict != core.TVCompatNeedsRemux {
		t.Fatalf("pending[0] = %+v, want newer remux file %d", empty.Pending[0], remux.ID)
	}
	if empty.Pending[0].MovieID != remux.MovieID || empty.Pending[1].MovieID != file.MovieID {
		t.Fatalf("pending movie ids = %d, %d, want %d, %d",
			empty.Pending[0].MovieID, empty.Pending[1].MovieID, remux.MovieID, file.MovieID)
	}
	if empty.Pending[1].ID != file.ID ||
		empty.Pending[1].Compatibility.Verdict != core.TVCompatIncompatible {
		t.Fatalf("pending[1] = %+v, want older incompatible file %d", empty.Pending[1], file.ID)
	}

	rec = do(t, h, "POST", "/api/v1/convert", `{"media_file_id":`+itoa(file.ID)+`}`)
	wantStatus(t, rec, http.StatusCreated)
	var created conversionJSON
	decodeBody(t, rec, &created)
	if created.Status != core.ConversionQueued {
		t.Fatalf("status = %q, want queued", created.Status)
	}
	if created.SourcePath != file.Path {
		t.Fatalf("source_path = %q, want %q", created.SourcePath, file.Path)
	}
	// The profile is recorded at queue time so a later change cannot rewrite
	// what this conversion was aimed at.
	if created.ProfileID != core.TVProfileSafe {
		t.Fatalf("profile_id = %q, want the active profile", created.ProfileID)
	}

	// A durable job now exists for it: the queue is at-least-once, not a
	// goroutine the HTTP handler spawned.
	jobs, err := st.ListJobs(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	found := false
	for _, job := range jobs {
		if job.Kind != convert.JobKind {
			continue
		}
		var payload convert.Payload
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
			t.Fatalf("job payload: %v", err)
		}
		if payload.ConversionID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s job was enqueued for conversion %d", convert.JobKind, created.ID)
	}

	rec = do(t, h, "GET", "/api/v1/convert", "")
	wantStatus(t, rec, http.StatusOK)
	var listed struct {
		Pending     []mediaFileJSON  `json:"pending"`
		Conversions []conversionJSON `json:"conversions"`
	}
	decodeBody(t, rec, &listed)
	if len(listed.Conversions) != 1 || listed.Conversions[0].ID != created.ID {
		t.Fatalf("queue = %+v", listed.Conversions)
	}
	if len(listed.Pending) != 1 || listed.Pending[0].ID != remux.ID {
		t.Fatalf("pending after queue = %+v, want only remux file %d", listed.Pending, remux.ID)
	}
	if listed.Conversions[0].MovieID != file.MovieID {
		t.Fatalf("queued conversion movie_id = %d, want %d", listed.Conversions[0].MovieID, file.MovieID)
	}
}

func TestConvertQueueUsesItemPlaybackTarget(t *testing.T) {
	h, st, _ := newTestServer(t, WithConverter(stubConverter{available: true}))
	ctx := t.Context()
	movie := &core.Movie{TMDBID: 20, Title: "Modern Movie", SortTitle: "modern movie"}
	if err := st.UpsertMovie(ctx, movie); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	file := &core.MediaFile{
		Path: "library/Movies/Modern Movie/Modern Movie.mkv", MovieID: movie.ID,
		Codec: "x265", Audio: "AAC", Quality: core.Quality2160p,
	}
	if err := st.UpsertMediaFile(ctx, file); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}

	listPending := func() []mediaFileJSON {
		t.Helper()
		rec := do(t, h, http.MethodGet, "/api/v1/convert", "")
		wantStatus(t, rec, http.StatusOK)
		var body struct {
			Pending []mediaFileJSON `json:"pending"`
		}
		decodeBody(t, rec, &body)
		return body.Pending
	}
	if pending := listPending(); len(pending) != 1 || pending[0].ID != file.ID {
		t.Fatalf("safe-target pending = %+v, want file %d", pending, file.ID)
	}

	assignMoviePlaybackTarget(t, st, movie.ID, core.TVProfileCapable)
	if pending := listPending(); len(pending) != 0 {
		t.Fatalf("capable-target pending = %+v, want none", pending)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/convert", `{"media_file_id":`+itoa(file.ID)+`}`)
	wantStatus(t, rec, http.StatusCreated)
	var created conversionJSON
	decodeBody(t, rec, &created)
	if created.ProfileID != core.TVProfileCapable {
		t.Fatalf("queued playback target = %q, want %q", created.ProfileID, core.TVProfileCapable)
	}
}

func TestConvertQueueNamesEpisodeTargets(t *testing.T) {
	h, st, _ := newTestServer(t, WithConverter(stubConverter{available: true}))
	ctx := context.Background()
	series := &core.Series{TMDBID: 3, Title: "Severance", SortTitle: "severance"}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	episode := &core.Episode{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 2, Title: "Half Loop"}
	if err := st.UpsertEpisode(ctx, episode); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	file := core.MediaFile{
		Path: "library/TV/Severance (2022)/Season 01/S01E02.mkv", Size: 1234,
		Codec: "x265", Audio: "DTS", Quality: core.Quality2160p,
	}
	if err := st.UpsertMediaFile(ctx, &file); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	if err := st.LinkEpisodeFile(ctx, episode.ID, file.ID); err != nil {
		t.Fatalf("LinkEpisodeFile: %v", err)
	}

	rec := do(t, h, "GET", "/api/v1/convert", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Pending []mediaFileJSON `json:"pending"`
	}
	decodeBody(t, rec, &body)
	if len(body.Pending) != 1 {
		t.Fatalf("pending = %+v, want the episode file", body.Pending)
	}
	got := body.Pending[0]
	if got.SeriesID != series.ID || got.SeriesKind != core.SeriesKindTV ||
		got.SeasonNumber != 1 || got.EpisodeNumber != 2 || got.MovieID != 0 {
		t.Fatalf("pending target = %+v, want series %d S01E02", got, series.ID)
	}
}

func TestConvertListIncludesLiveProgress(t *testing.T) {
	progress := make(map[int64]convert.LiveProgress)
	h, st, _ := newTestServer(t, WithConverter(stubConverter{
		available: true,
		progress:  progress,
	}))
	file := seedConvertibleFile(t, st, "library/Movies/Live (2026)/Live (2026).mkv")
	conv := &core.Conversion{
		MediaFileID: file.ID,
		SourcePath:  file.Path,
		Strategy:    core.ConvertStrategyTranscode,
		ProfileID:   core.TVProfileSafe,
		Status:      core.ConversionRunning,
	}
	if err := st.CreateConversion(t.Context(), conv); err != nil {
		t.Fatalf("CreateConversion: %v", err)
	}
	started := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	progress[conv.ID] = convert.LiveProgress{
		Stage:            convert.ProgressStageConverting,
		StartedAt:        started,
		DurationSeconds:  120,
		ProcessedSeconds: 30,
		Speed:            1.5,
	}

	rec := do(t, h, "GET", "/api/v1/convert", "")
	wantStatus(t, rec, http.StatusOK)
	var listed struct {
		Conversions []conversionJSON `json:"conversions"`
	}
	decodeBody(t, rec, &listed)
	if len(listed.Conversions) != 1 {
		t.Fatalf("conversions = %+v, want one", listed.Conversions)
	}
	got := listed.Conversions[0]
	if got.Stage != convert.ProgressStageConverting ||
		!got.StartedAt.Equal(started) ||
		got.Progress != 0.25 ||
		got.ProcessedSeconds != 30 ||
		got.DurationSeconds != 120 ||
		got.Speed != 1.5 ||
		got.ETASeconds != 60 {
		t.Fatalf("conversion progress = %+v", got)
	}

	progress[conv.ID] = convert.LiveProgress{
		Stage:            convert.ProgressStageVerifying,
		StartedAt:        started,
		DurationSeconds:  120,
		ProcessedSeconds: 30,
		Speed:            1.5,
	}
	rec = do(t, h, "GET", "/api/v1/convert", "")
	wantStatus(t, rec, http.StatusOK)
	listed.Conversions = nil
	decodeBody(t, rec, &listed)
	got = listed.Conversions[0]
	if got.Stage != convert.ProgressStageVerifying ||
		got.Progress != 0.25 ||
		got.Speed != 0 ||
		got.ETASeconds != 0 {
		t.Fatalf("verification progress = %+v, want fraction without speed or ETA", got)
	}

	conv.Status = core.ConversionDone
	if err := st.UpdateConversion(t.Context(), conv); err != nil {
		t.Fatalf("UpdateConversion(done): %v", err)
	}
	rec = do(t, h, "GET", "/api/v1/convert", "")
	wantStatus(t, rec, http.StatusOK)
	var terminal struct {
		Conversions []map[string]json.RawMessage `json:"conversions"`
	}
	decodeBody(t, rec, &terminal)
	if len(terminal.Conversions) != 1 {
		t.Fatalf("terminal conversions = %+v, want one", terminal.Conversions)
	}
	for _, field := range []string{
		"stage", "started_at", "progress", "processed_seconds",
		"duration_seconds", "speed", "eta_seconds",
	} {
		if _, ok := terminal.Conversions[0][field]; ok {
			t.Fatalf("terminal conversion includes live field %q", field)
		}
	}
}

func TestConversionSettingsRoundTripAndValidation(t *testing.T) {
	h, st, _ := newTestServer(t)

	rec := do(t, h, http.MethodPut, "/api/v1/settings", `{
		"convert_video_preset":" slow ",
		"convert_video_crf":"18",
		"convert_audio_bitrate_kbps":"256"
	}`)
	wantStatus(t, rec, http.StatusOK)
	var settings map[string]string
	decodeBody(t, rec, &settings)
	want := map[string]string{
		store.SettingConvertVideoPreset:      "slow",
		store.SettingConvertVideoCRF:         "18",
		store.SettingConvertAudioBitrateKbps: "256",
	}
	for key, value := range want {
		if settings[key] != value {
			t.Errorf("%s = %q, want %q", key, settings[key], value)
		}
	}

	for _, body := range []string{
		`{"convert_video_preset":"turbo"}`,
		`{"convert_video_crf":"-1"}`,
		`{"convert_video_crf":"52"}`,
		`{"convert_audio_bitrate_kbps":"63"}`,
		`{"convert_audio_bitrate_kbps":"513"}`,
		`{"convert_video_preset":"medium","convert_video_crf":"52"}`,
	} {
		rec = do(t, h, http.MethodPut, "/api/v1/settings", body)
		wantStatus(t, rec, http.StatusBadRequest)
		wantErrorBody(t, rec)
	}
	stored, err := st.GetSetting(t.Context(), store.SettingConvertVideoPreset)
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if stored != "slow" {
		t.Fatalf("invalid partial update changed preset to %q, want slow", stored)
	}
}

func TestConvertPendingRespectsAdultVisibility(t *testing.T) {
	h, st, _ := newTestServer(t, WithConverter(stubConverter{available: true}))
	ctx := t.Context()
	site := &core.Series{
		Kind: core.SeriesKindAdult, StashID: "site-1", Title: "Site", SortTitle: "site",
		LibraryID: defaultLibraryID(t, st, core.LibraryKindAdult),
	}
	if err := st.UpsertSeries(ctx, site); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	scene := &core.Episode{SeriesID: site.ID, SeasonNumber: 2026, EpisodeNumber: 1}
	if err := st.UpsertEpisode(ctx, scene); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	file := &core.MediaFile{
		Path: "library/Adult/Site/2026/Scene.mkv", Codec: "x265", Audio: "DTS",
		Quality: core.Quality2160p,
	}
	if err := st.UpsertMediaFile(ctx, file); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	if err := st.LinkEpisodeFile(ctx, scene.ID, file.ID); err != nil {
		t.Fatalf("LinkEpisodeFile: %v", err)
	}

	listPending := func() []mediaFileJSON {
		t.Helper()
		rec := do(t, h, "GET", "/api/v1/convert", "")
		wantStatus(t, rec, http.StatusOK)
		var body struct {
			Pending []mediaFileJSON `json:"pending"`
		}
		decodeBody(t, rec, &body)
		return body.Pending
	}
	if got := listPending(); len(got) != 0 {
		t.Fatalf("pending with adult module off = %+v, want empty", got)
	}

	enableAdultLibrary(t, st)
	got := listPending()
	if len(got) != 1 || got[0].ID != file.ID {
		t.Fatalf("pending with adult module on = %+v, want file %d", got, file.ID)
	}
}

func TestConvertRejectsASecondQueueForTheSameFile(t *testing.T) {
	h, st, _ := newTestServer(t, WithConverter(stubConverter{available: true}))
	file := seedConvertibleFile(t, st, "library/Movies/B (2002)/B (2002).mkv")
	body := `{"media_file_id":` + itoa(file.ID) + `}`

	wantStatus(t, do(t, h, "POST", "/api/v1/convert", body), http.StatusCreated)

	rec := do(t, h, "POST", "/api/v1/convert", body)
	wantStatus(t, rec, http.StatusConflict)
	wantErrorBody(t, rec)
}

func TestConvertValidatesItsInput(t *testing.T) {
	h, _, _ := newTestServer(t, WithConverter(stubConverter{available: true}))

	tests := []struct {
		name string
		body string
		want int
	}{
		{"no id", `{}`, http.StatusBadRequest},
		{"negative id", `{"media_file_id":-1}`, http.StatusBadRequest},
		{"unknown file", `{"media_file_id":404}`, http.StatusNotFound},
		{"garbage", `not json`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, "POST", "/api/v1/convert", tc.body)
			wantStatus(t, rec, tc.want)
			wantErrorBody(t, rec)
		})
	}
}

// TestConvertDegradesWithoutFFmpeg is SPEC §8's graceful degradation: no
// ffmpeg means the affordance is unavailable, not that the API breaks.
func TestConvertDegradesWithoutFFmpeg(t *testing.T) {
	for _, name := range []string{"no converter wired", "converter reports unavailable"} {
		t.Run(name, func(t *testing.T) {
			opts := []Option{}
			if name != "no converter wired" {
				opts = append(opts, WithConverter(stubConverter{available: false}))
			}
			h, st, _ := newTestServer(t, opts...)
			file := seedConvertibleFile(t, st, "library/Movies/C (2003)/C (2003).mkv")

			rec := do(t, h, "POST", "/api/v1/convert", `{"media_file_id":`+itoa(file.ID)+`}`)
			wantStatus(t, rec, http.StatusServiceUnavailable)
			wantErrorBody(t, rec)

			// The record of what ffmpeg did while it was installed stays
			// readable, so uninstalling it does not erase history.
			wantStatus(t, do(t, h, "GET", "/api/v1/convert", ""), http.StatusOK)

			var status statusResponse
			rec = do(t, h, "GET", "/api/v1/system/status", "")
			wantStatus(t, rec, http.StatusOK)
			decodeBody(t, rec, &status)
			if status.FFmpegAvailable {
				t.Fatal("system status claims ffmpeg is available when it is not")
			}
		})
	}
}

func TestSystemStatusReportsFFmpeg(t *testing.T) {
	h, _, _ := newTestServer(t, WithConverter(stubConverter{available: true}))
	rec := do(t, h, "GET", "/api/v1/system/status", "")
	wantStatus(t, rec, http.StatusOK)

	var status statusResponse
	decodeBody(t, rec, &status)
	if !status.FFmpegAvailable {
		t.Fatal("ffmpeg_available = false, want true")
	}
}

func TestCancelConversion(t *testing.T) {
	h, st, _ := newTestServer(t, WithConverter(stubConverter{available: true}))
	file := seedConvertibleFile(t, st, "library/Movies/D (2004)/D (2004).mkv")

	rec := do(t, h, "POST", "/api/v1/convert", `{"media_file_id":`+itoa(file.ID)+`}`)
	wantStatus(t, rec, http.StatusCreated)
	var created conversionJSON
	decodeBody(t, rec, &created)

	rec = do(t, h, "POST", "/api/v1/convert/"+itoa(created.ID)+"/cancel", "")
	wantStatus(t, rec, http.StatusOK)
	var cancelled conversionJSON
	decodeBody(t, rec, &cancelled)
	if cancelled.Status != core.ConversionCancelled {
		t.Fatalf("status = %q, want cancelled", cancelled.Status)
	}

	// Cancelling twice is a conflict, not a silent success.
	rec = do(t, h, "POST", "/api/v1/convert/"+itoa(created.ID)+"/cancel", "")
	wantStatus(t, rec, http.StatusConflict)

	// Cancelling frees the file for a fresh conversion.
	wantStatus(t, do(t, h, "POST", "/api/v1/convert", `{"media_file_id":`+itoa(file.ID)+`}`), http.StatusCreated)
}

func TestCancelRunningConversionIsRefused(t *testing.T) {
	h, st, _ := newTestServer(t, WithConverter(stubConverter{available: true}))
	ctx := context.Background()
	file := seedConvertibleFile(t, st, "library/Movies/E (2005)/E (2005).mkv")

	conv := &core.Conversion{MediaFileID: file.ID, SourcePath: file.Path, Status: core.ConversionRunning}
	if err := st.CreateConversion(ctx, conv); err != nil {
		t.Fatalf("CreateConversion: %v", err)
	}

	rec := do(t, h, "POST", "/api/v1/convert/"+itoa(conv.ID)+"/cancel", "")
	wantStatus(t, rec, http.StatusConflict)
	wantErrorBody(t, rec)
}

func TestRetryConversion(t *testing.T) {
	h, st, _ := newTestServer(t, WithConverter(stubConverter{available: true}))
	ctx := context.Background()
	file := seedConvertibleFile(t, st, "library/Movies/F (2006)/F (2006).mkv")

	conv := &core.Conversion{
		MediaFileID: file.ID, SourcePath: file.Path,
		Status: core.ConversionFailed, Error: "ffmpeg: Invalid data found",
	}
	if err := st.CreateConversion(ctx, conv); err != nil {
		t.Fatalf("CreateConversion: %v", err)
	}

	rec := do(t, h, "POST", "/api/v1/convert/"+itoa(conv.ID)+"/retry", "")
	wantStatus(t, rec, http.StatusOK)
	var retried conversionJSON
	decodeBody(t, rec, &retried)
	if retried.Status != core.ConversionQueued || retried.Error != "" {
		t.Fatalf("retried = %+v, want queued with the error cleared", retried)
	}

	// Retrying something already in the queue is a conflict.
	rec = do(t, h, "POST", "/api/v1/convert/"+itoa(conv.ID)+"/retry", "")
	wantStatus(t, rec, http.StatusConflict)

	// A fresh durable job now exists: the failed one had spent its attempts.
	jobs, err := st.ListJobs(ctx, 0)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	convertJobs := 0
	for _, job := range jobs {
		if job.Kind == convert.JobKind {
			convertJobs++
		}
	}
	if convertJobs != 1 {
		t.Fatalf("%d convert jobs, want 1", convertJobs)
	}
}

func TestRetryDoneConversionIsRefused(t *testing.T) {
	h, st, _ := newTestServer(t, WithConverter(stubConverter{available: true}))
	ctx := context.Background()
	file := seedConvertibleFile(t, st, "library/Movies/G (2007)/G (2007).mp4")

	conv := &core.Conversion{MediaFileID: file.ID, SourcePath: file.Path, Status: core.ConversionDone}
	if err := st.CreateConversion(ctx, conv); err != nil {
		t.Fatalf("CreateConversion: %v", err)
	}

	rec := do(t, h, "POST", "/api/v1/convert/"+itoa(conv.ID)+"/retry", "")
	wantStatus(t, rec, http.StatusConflict)
	wantErrorBody(t, rec)
}

func TestConversionEndpointsRejectUnknownIDs(t *testing.T) {
	h, _, _ := newTestServer(t, WithConverter(stubConverter{available: true}))
	for _, path := range []string{"/api/v1/convert/999/cancel", "/api/v1/convert/999/retry"} {
		rec := do(t, h, "POST", path, "")
		wantStatus(t, rec, http.StatusNotFound)
		wantErrorBody(t, rec)
	}
	rec := do(t, h, "GET", "/api/v1/convert?limit=0", "")
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
}
