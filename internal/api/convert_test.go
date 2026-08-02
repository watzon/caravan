package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/watzon/caravan/internal/convert"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// stubConverter stands in for internal/convert. Availability is the only thing
// the HTTP layer asks it, so it is the only thing the stub answers.
type stubConverter struct{ available bool }

func (c stubConverter) Available() bool { return c.available }

func seedConvertibleFile(t *testing.T, st *store.Store, path string) *core.MediaFile {
	t.Helper()
	f := core.MediaFile{Path: path, Size: 1234, Codec: "x265", Audio: "DTS", Quality: core.Quality2160p}
	if err := st.UpsertMediaFile(context.Background(), &f); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	return &f
}

func TestConvertQueueRoundTrip(t *testing.T) {
	h, st, _ := newTestServer(t, WithConverter(stubConverter{available: true}))
	file := seedConvertibleFile(t, st, "library/Movies/A (2001)/A (2001).mkv")

	// Empty to start with, and the envelope is a list, never null.
	rec := do(t, h, "GET", "/api/v1/convert", "")
	wantStatus(t, rec, http.StatusOK)
	var empty struct {
		Conversions []conversionJSON `json:"conversions"`
	}
	decodeBody(t, rec, &empty)
	if empty.Conversions == nil || len(empty.Conversions) != 0 {
		t.Fatalf("conversions = %v, want an empty list", empty.Conversions)
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
		Conversions []conversionJSON `json:"conversions"`
	}
	decodeBody(t, rec, &listed)
	if len(listed.Conversions) != 1 || listed.Conversions[0].ID != created.ID {
		t.Fatalf("queue = %+v", listed.Conversions)
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
