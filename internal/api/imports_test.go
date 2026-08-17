package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/library"
	"github.com/watzon/caravan/internal/store"
)

// parkFile puts one file in the scan-review queue and returns it.
func parkFile(t *testing.T, st *store.Store, path string, parsed core.ParsedRelease) *core.UnmatchedFile {
	t.Helper()
	u := &core.UnmatchedFile{Path: path, Size: 1234, Parsed: parsed, Reason: "no metadata match"}
	if err := st.UpsertUnmatchedFile(context.Background(), u); err != nil {
		t.Fatalf("UpsertUnmatchedFile: %v", err)
	}
	return u
}

func TestImportQueueListsParsedGuess(t *testing.T) {
	h, st, _ := newTestServer(t)

	u := parkFile(t, st, "incoming/Some.Show.S01E02E03.1080p.WEB-DL.x265-GRP.mkv", core.ParsedRelease{
		Title:      "Some Show",
		Season:     1,
		Episodes:   []int{2, 3},
		Quality:    core.Quality1080p,
		Source:     core.SourceWebDL,
		Codec:      "x265",
		Group:      "GRP",
		Confidence: 0.4,
	})

	rec := do(t, h, http.MethodGet, "/api/v1/import/queue", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Items []unmatchedJSON `json:"items"`
	}
	decodeBody(t, rec, &body)

	if len(body.Items) != 1 {
		t.Fatalf("items = %+v, want 1", body.Items)
	}
	got := body.Items[0]
	if got.ID != u.ID || got.Path != u.Path || got.Size != 1234 || got.Reason != "no metadata match" {
		t.Fatalf("item = %+v, want the parked file", got)
	}
	if got.SeenAt == "" {
		t.Fatalf("item.SeenAt is empty, want a timestamp")
	}
	want := parsedJSON{
		Title: "Some Show", Season: 1, Episodes: []int{2, 3},
		Quality: core.Quality1080p, Source: core.SourceWebDL, Codec: "x265",
		Group: "GRP", Confidence: 0.4,
	}
	if got.Parsed.Title != want.Title || got.Parsed.Season != want.Season ||
		len(got.Parsed.Episodes) != 2 || got.Parsed.Episodes[1] != 3 ||
		got.Parsed.Quality != want.Quality || got.Parsed.Source != want.Source ||
		got.Parsed.Codec != want.Codec || got.Parsed.Group != want.Group ||
		got.Parsed.Confidence != want.Confidence {
		t.Fatalf("parsed = %+v, want %+v", got.Parsed, want)
	}
}

func TestImportQueueRepairsObfuscatedAdultParserGuess(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	// The Adult library migration 0011 seeds, switched on: `active` is the
	// module's on-switch now that the row is always there.
	adult, err := st.GetLibraryByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetLibraryByKind(adult): %v", err)
	}
	if err := st.SetLibraryActive(ctx, adult.ID, true); err != nil {
		t.Fatalf("SetLibraryActive: %v", err)
	}

	const release = "AfricanCasting.20.01.26.Scarlet.XXX.1080p.MP4-WRB"
	u := &core.UnmatchedFile{
		Path:      "incomplete/" + release + "/006ae62d1d9d452cab14e9c02e932a6a.mp4",
		Size:      1234,
		Parsed:    core.ParsedRelease{Title: "006ae62d1d9d452cab14e9c02e932a6a", Confidence: 0.25},
		Reason:    library.ReasonImport,
		LibraryID: adult.ID,
	}
	if err := st.UpsertUnmatchedFile(ctx, u); err != nil {
		t.Fatalf("UpsertUnmatchedFile: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/import/queue", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Items []unmatchedJSON `json:"items"`
	}
	decodeBody(t, rec, &body)
	if len(body.Items) != 1 {
		t.Fatalf("items = %+v, want repaired row", body.Items)
	}
	got := body.Items[0].Parsed
	if got.Title != "AfricanCasting" || got.Year != 2020 ||
		got.SceneDate != "2020-01-26T00:00:00Z" ||
		got.Quality != core.Quality1080p || got.Group != "WRB" {
		t.Errorf("parser guess = %+v, want release directory metadata and scene date", got)
	}

	stored, err := st.GetUnmatchedFile(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUnmatchedFile: %v", err)
	}
	if stored.Parsed.Title != "AfricanCasting" || stored.Parsed.SceneDate.IsZero() {
		t.Errorf("stored parser guess = %+v, want repaired parse persisted", stored.Parsed)
	}
}

func TestImportQueueEmpty(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodGet, "/api/v1/import/queue", "")
	wantStatus(t, rec, http.StatusOK)
	if got := rec.Body.String(); got != "{\"items\":[]}\n" {
		t.Fatalf("body = %q, want an empty items array", got)
	}
}

func TestImportMatch(t *testing.T) {
	h, st, mgr := newTestServer(t)
	u := parkFile(t, st, "incoming/movie.mkv", core.ParsedRelease{Title: "Movie"})

	rec := do(t, h, http.MethodPost, "/api/v1/import/queue/"+itoa(u.ID)+"/match",
		`{"type":"movie","tmdb_id":603}`)
	wantStatus(t, rec, http.StatusOK)

	calls := mgr.matchCalls()
	if len(calls) != 1 {
		t.Fatalf("match calls = %+v, want 1", calls)
	}
	// A tmdb_id body still resolves to a TMDB ref, spelled out: that is the
	// compatibility path, and it has to keep pinning to the provider it always
	// pinned to.
	want := matchCall{id: u.ID, mediaType: MediaTypeMovie, tmdbID: 603, ref: core.TMDBRef(603)}
	if calls[0] != want {
		t.Fatalf("match call = %+v, want %+v", calls[0], want)
	}
}

func TestImportMatchScene(t *testing.T) {
	h, st, mgr := newTestServer(t)
	enableAdult(t, st)
	seedStashboxInstance(t, st, core.ProviderStashbox, "StashDB", "https://stashdb.org/graphql")
	u := parkFile(t, st, "incomplete/release/hash.mp4", core.ParsedRelease{Title: "Site"})

	rec := do(t, h, http.MethodPost, "/api/v1/import/queue/"+itoa(u.ID)+"/match",
		`{"type":"scene","provider":"stashbox","provider_ref":"scene-1"}`)
	wantStatus(t, rec, http.StatusOK)

	calls := mgr.matchCalls()
	want := matchCall{
		id: u.ID, mediaType: MediaTypeScene,
		ref: core.ItemRef{Provider: core.ProviderStashbox, Ref: "scene-1"},
	}
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("match calls = %+v, want %+v", calls, want)
	}
}

// A manual match names the title the same way an add does. The parked file is
// resolved against the ref the user picked, not against a TMDB id the body may
// never have carried.
func TestImportMatchByProviderRef(t *testing.T) {
	h, st, mgr := newTestServer(t)
	u := parkFile(t, st, "incoming/show.mkv", core.ParsedRelease{Title: "Frieren", Season: 1})

	rec := do(t, h, http.MethodPost, "/api/v1/import/queue/"+itoa(u.ID)+"/match",
		`{"type":"series","provider":"anilist","provider_ref":"154587"}`)
	wantStatus(t, rec, http.StatusOK)

	calls := mgr.matchCalls()
	want := matchCall{id: u.ID, mediaType: MediaTypeSeries,
		ref: core.ItemRef{Provider: core.ProviderAniList, Ref: "154587"}}
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("match calls = %+v, want %+v", calls, want)
	}
}

// The same four refusals the add endpoints give, and the kind is the media type
// the body named: a movie match may not be pinned to a television-only provider.
func TestImportMatchRefValidationRefusals(t *testing.T) {
	h, st, mgr := newTestServer(t)
	u := parkFile(t, st, "incoming/movie.mkv", core.ParsedRelease{Title: "Movie"})
	id := itoa(u.ID)

	for name, body := range map[string]string{
		"provider without a ref":     `{"type":"series","provider":"anilist"}`,
		"ref without a provider":     `{"type":"series","provider_ref":"154587"}`,
		"neither spelling":           `{"type":"movie"}`,
		"provider of the wrong kind": `{"type":"movie","provider":"tvmaze","provider_ref":"1"}`,
	} {
		rec := do(t, h, http.MethodPost, "/api/v1/import/queue/"+id+"/match", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
	}
	if calls := mgr.matchCalls(); len(calls) != 0 {
		t.Fatalf("a refused match still reached the manager: %+v", calls)
	}
}

func TestImportMatchRejectsBadRequests(t *testing.T) {
	h, st, mgr := newTestServer(t)
	u := parkFile(t, st, "incoming/movie.mkv", core.ParsedRelease{Title: "Movie"})
	id := itoa(u.ID)

	tests := []struct {
		name       string
		path       string
		body       string
		wantStatus int
	}{
		{"unknown media type", "/api/v1/import/queue/" + id + "/match", `{"type":"album","tmdb_id":1}`, http.StatusBadRequest},
		{"missing media type", "/api/v1/import/queue/" + id + "/match", `{"tmdb_id":1}`, http.StatusBadRequest},
		{"missing tmdb id", "/api/v1/import/queue/" + id + "/match", `{"type":"movie"}`, http.StatusBadRequest},
		{"malformed json", "/api/v1/import/queue/" + id + "/match", `{`, http.StatusBadRequest},
		{"bad id", "/api/v1/import/queue/nope/match", `{"type":"movie","tmdb_id":1}`, http.StatusBadRequest},
		{"unknown id", "/api/v1/import/queue/9999/match", `{"type":"movie","tmdb_id":1}`, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, tt.path, tt.body)
			wantStatus(t, rec, tt.wantStatus)
			wantErrorBody(t, rec)
		})
	}

	if calls := mgr.matchCalls(); len(calls) != 0 {
		t.Fatalf("match calls = %+v, want none from rejected requests", calls)
	}
}

func TestImportMatchReportsManagerFailure(t *testing.T) {
	h, st, mgr := newTestServer(t)
	u := parkFile(t, st, "incoming/movie.mkv", core.ParsedRelease{Title: "Movie"})
	mgr.matchErr = errors.New("hardlink failed")

	rec := do(t, h, http.MethodPost, "/api/v1/import/queue/"+itoa(u.ID)+"/match",
		`{"type":"movie","tmdb_id":603}`)
	wantStatus(t, rec, http.StatusBadGateway)
	wantErrorBody(t, rec)
}

func TestImportDelete(t *testing.T) {
	h, st, _ := newTestServer(t)
	u := parkFile(t, st, "incoming/junk.mkv", core.ParsedRelease{})

	rec := do(t, h, http.MethodDelete, "/api/v1/import/queue/"+itoa(u.ID), "")
	wantStatus(t, rec, http.StatusNoContent)

	files, err := st.ListUnmatchedFiles(context.Background())
	if err != nil {
		t.Fatalf("ListUnmatchedFiles: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("queue = %+v, want it emptied", files)
	}

	// Deleting again is a 404, not a silent success.
	rec = do(t, h, http.MethodDelete, "/api/v1/import/queue/"+itoa(u.ID), "")
	wantStatus(t, rec, http.StatusNotFound)
	wantErrorBody(t, rec)
}
