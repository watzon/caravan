package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

func pastDate() time.Time   { return time.Now().UTC().AddDate(0, 0, -7) }
func futureDate() time.Time { return time.Now().UTC().AddDate(0, 0, 7) }

func TestQualityProfileCRUD(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	// The seeded default is always there.
	rec := do(t, h, http.MethodGet, "/api/v1/quality-profiles", "")
	wantStatus(t, rec, http.StatusOK)
	var list struct {
		Profiles []qualityProfileJSON `json:"profiles"`
	}
	decodeBody(t, rec, &list)
	if len(list.Profiles) != 1 || list.Profiles[0].Name != "Standard" {
		t.Fatalf("profiles = %+v, want the seeded Standard profile", list.Profiles)
	}

	rec = do(t, h, http.MethodPost, "/api/v1/quality-profiles",
		`{"name":"HD only","cutoff":"1080p","items":["2160p","1080p"],"upgrade_allowed":true}`)
	wantStatus(t, rec, http.StatusCreated)
	var created qualityProfileJSON
	decodeBody(t, rec, &created)
	if created.ID == 0 {
		t.Fatal("created profile has no id")
	}

	// A duplicate name is a conflict, not a 500.
	rec = do(t, h, http.MethodPost, "/api/v1/quality-profiles",
		`{"name":"HD only","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true}`)
	wantStatus(t, rec, http.StatusConflict)

	// The cutoff must be reachable from the items.
	rec = do(t, h, http.MethodPost, "/api/v1/quality-profiles",
		`{"name":"broken","cutoff":"720p","items":["1080p"],"upgrade_allowed":true}`)
	wantStatus(t, rec, http.StatusBadRequest)

	rec = do(t, h, http.MethodPut, "/api/v1/quality-profiles/"+itoa(created.ID),
		`{"name":"HD only","cutoff":"1080p","items":["1080p","720p"],"upgrade_allowed":false}`)
	wantStatus(t, rec, http.StatusOK)
	var updated qualityProfileJSON
	decodeBody(t, rec, &updated)
	if updated.UpgradeAllowed || len(updated.Items) != 2 {
		t.Fatalf("updated profile = %+v", updated)
	}

	// The default profile is protected; any other deletable.
	defaults, err := st.ListQualityProfiles(ctx)
	if err != nil {
		t.Fatalf("ListQualityProfiles: %v", err)
	}
	rec = do(t, h, http.MethodDelete, "/api/v1/quality-profiles/"+itoa(defaults[0].ID), "")
	wantStatus(t, rec, http.StatusConflict)
	rec = do(t, h, http.MethodDelete, "/api/v1/quality-profiles/"+itoa(created.ID), "")
	wantStatus(t, rec, http.StatusNoContent)
}

func TestResolveQualityProfileFallsBackToDefault(t *testing.T) {
	h, st, _ := newTestServer(t)
	_ = h
	ctx := context.Background()

	// id 0 and a dangling id both land on the default.
	def, err := st.ResolveQualityProfile(ctx, 0)
	if err != nil {
		t.Fatalf("ResolveQualityProfile(0): %v", err)
	}
	if def.Name != "Standard" {
		t.Fatalf("default = %q, want Standard", def.Name)
	}
	again, err := st.ResolveQualityProfile(ctx, 999)
	if err != nil {
		t.Fatalf("ResolveQualityProfile(999): %v", err)
	}
	if again.ID != def.ID {
		t.Fatalf("dangling id resolved to %d, want default %d", again.ID, def.ID)
	}
}

func TestWantedList(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	missing := &core.Movie{TMDBID: 1, Title: "Missing Movie", Monitored: true}
	if err := st.UpsertMovie(ctx, missing); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	owned := &core.Movie{TMDBID: 2, Title: "Owned Movie", Monitored: true}
	if err := st.UpsertMovie(ctx, owned); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	// The default profile's cutoff is 1080p, so a 720p file keeps the movie
	// wanted as below-cutoff; a 1080p file settles it.
	if err := st.UpsertMediaFile(ctx, &core.MediaFile{
		Path: "Movies/Owned Movie (2020)/Owned Movie (2020).mkv", MovieID: owned.ID,
		Quality: core.Quality720p, Source: core.SourceWebDL,
	}); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	unmonitored := &core.Movie{TMDBID: 3, Title: "Skipped Movie", Monitored: false}
	if err := st.UpsertMovie(ctx, unmonitored); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	sr := &core.Series{TMDBID: 9, Title: "Andor", Monitored: true}
	if err := st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	aired := &core.Episode{SeriesID: sr.ID, SeasonNumber: 1, EpisodeNumber: 1,
		Title: "Kassa", Monitored: true, AirDate: pastDate()}
	if err := st.UpsertEpisode(ctx, aired); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	unaired := &core.Episode{SeriesID: sr.ID, SeasonNumber: 1, EpisodeNumber: 2,
		Title: "Future", Monitored: true, AirDate: futureDate()}
	if err := st.UpsertEpisode(ctx, unaired); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/wanted", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Movies   []wantedMovieJSON   `json:"movies"`
		Episodes []wantedEpisodeJSON `json:"episodes"`
	}
	decodeBody(t, rec, &body)

	if len(body.Movies) != 2 {
		t.Fatalf("wanted movies = %+v, want 2 (missing + below cutoff)", body.Movies)
	}
	reasons := map[string]string{}
	for _, m := range body.Movies {
		reasons[m.Title] = m.Reason
	}
	if reasons["Missing Movie"] != "missing" {
		t.Fatalf("Missing Movie reason = %q", reasons["Missing Movie"])
	}
	if reasons["Owned Movie"] != "below_cutoff" {
		t.Fatalf("Owned Movie reason = %q", reasons["Owned Movie"])
	}

	if len(body.Episodes) != 1 || body.Episodes[0].Title != "Kassa" {
		t.Fatalf("wanted episodes = %+v, want only the aired one", body.Episodes)
	}
}
