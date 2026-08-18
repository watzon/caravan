package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

type tvProfilesResponse struct {
	Profiles []tvProfileJSON `json:"profiles"`
}

func TestListTVProfilesReturnsPlaybackTargets(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodGet, "/api/v1/tv-profiles", "")
	wantStatus(t, rec, http.StatusOK)
	var body tvProfilesResponse
	decodeBody(t, rec, &body)

	if len(body.Profiles) < 2 {
		t.Fatalf("profiles = %d, want the safe and capable presets", len(body.Profiles))
	}
	safe := body.Profiles[0]
	if safe.MaxQuality != core.Quality1080p || safe.MaxBitDepth != 8 {
		t.Fatalf("safe profile = %+v, want 1080p / 8-bit (SPEC §8)", safe)
	}
}

func assignMoviePlaybackTarget(t *testing.T, st *store.Store, movieID int64, target string) *core.QualityProfile {
	t.Helper()
	profile := &core.QualityProfile{
		Name:           "Movie " + target,
		Cutoff:         core.Quality2160p,
		Items:          []string{core.Quality2160p, core.Quality1080p},
		UpgradeAllowed: true,
		TVProfile:      target,
	}
	if err := st.CreateQualityProfile(context.Background(), profile); err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}
	if err := st.SetMovieQualityProfile(context.Background(), movieID, profile.ID); err != nil {
		t.Fatalf("SetMovieQualityProfile: %v", err)
	}
	return profile
}

func TestSettingsRejectsGlobalPlaybackTarget(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodPut, "/api/v1/settings", `{"tv_profile":"capable"}`)
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
}

func TestReleasePickerFlagsAgainstTheItemPlaybackTarget(t *testing.T) {
	h, st, _, fake := newAcquisitionServer(t)
	m := addMovie(t, st, "Big Buck Bunny", 2008)
	addIndexer(t, st, fake, "alpha")

	fake.serve("alpha",
		// Parsed left empty on purpose: the API parses, so this exercises the
		// same path the real indexers take.
		torrentRelease("Big.Buck.Bunny.2008.1080p.BluRay.x265.10bit.DTS-HD.MA.7.1-GRP", "hevc", 50, core.ParsedRelease{}),
		torrentRelease("Big.Buck.Bunny.2008.1080p.WEB-DL.x264.AAC-GRP", "safe", 40, core.ParsedRelease{}),
	)

	rec := do(t, h, http.MethodGet, "/api/v1/library/movies/"+itoa(m.ID)+"/releases", "")
	wantStatus(t, rec, http.StatusOK)
	var body releasesResponse
	decodeBody(t, rec, &body)

	byGUID := map[string]releaseJSON{}
	for _, rel := range body.Releases {
		byGUID[rel.GUID] = rel
	}
	if len(byGUID) != 2 {
		t.Fatalf("releases = %d, want 2", len(byGUID))
	}

	flagged := byGUID["hevc"]
	if flagged.Compatibility.Verdict != core.TVCompatIncompatible {
		t.Fatalf("verdict = %q, want %q (reasons %v)",
			flagged.Compatibility.Verdict, core.TVCompatIncompatible, flagged.Compatibility.Reasons)
	}
	joined := strings.Join(flagged.Compatibility.Reasons, "; ")
	for _, want := range []string{"HEVC video", "10-bit video", "DTS-HD audio"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("reasons = %q, want it to name %q", joined, want)
		}
	}
	if flagged.Parsed.BitDepth != 10 {
		t.Fatalf("parsed bit_depth = %d, want 10", flagged.Parsed.BitDepth)
	}

	clean := byGUID["safe"]
	if clean.Compatibility.Verdict != core.TVCompatCompatible {
		t.Fatalf("verdict = %q, want %q for H.264/AAC (reasons %v)",
			clean.Compatibility.Verdict, core.TVCompatCompatible, clean.Compatibility.Reasons)
	}
	if len(clean.Compatibility.Reasons) != 0 {
		t.Fatalf("reasons = %v, want none", clean.Compatibility.Reasons)
	}
}

func TestReleasePickerUsesTheItemPlaybackTarget(t *testing.T) {
	h, st, _, fake := newAcquisitionServer(t)
	m := addMovie(t, st, "Big Buck Bunny", 2008)
	addIndexer(t, st, fake, "alpha")
	fake.serve(
		"alpha",
		torrentRelease(
			"Big.Buck.Bunny.2008.2160p.WEB-DL.x264.AAC-GRP",
			"uhd",
			50,
			core.ParsedRelease{},
		),
		torrentRelease(
			"Big.Buck.Bunny.2008.1080p.WEB-DL.x264.AAC-GRP",
			"hd",
			50,
			core.ParsedRelease{},
		),
	)

	search := func() releasesResponse {
		rec := do(
			t,
			h,
			http.MethodGet,
			"/api/v1/library/movies/"+itoa(m.ID)+"/releases",
			"",
		)
		wantStatus(t, rec, http.StatusOK)
		var body releasesResponse
		decodeBody(t, rec, &body)
		return body
	}

	safe := search()
	if len(safe.Releases) != 2 {
		t.Fatalf("safe-profile releases = %d, want 2", len(safe.Releases))
	}
	if safe.Releases[0].GUID != "hd" {
		t.Fatalf("safe-profile order = %v, want compatible 1080p first", titlesOf(safe.Releases))
	}
	if safe.Releases[1].Compatibility.Verdict != core.TVCompatIncompatible {
		t.Fatalf("2160p compatibility = %+v, want incompatible on safe profile", safe.Releases[1].Compatibility)
	}

	assignMoviePlaybackTarget(t, st, m.ID, core.TVProfileCapable)

	capable := search()
	if capable.Releases[0].GUID != "uhd" {
		t.Fatalf("capable-profile order = %v, want compatible 2160p first", titlesOf(capable.Releases))
	}
	if capable.Releases[0].Compatibility.Verdict != core.TVCompatCompatible {
		t.Fatalf("2160p compatibility = %+v, want compatible on capable profile", capable.Releases[0].Compatibility)
	}
}

func TestMovieFileCarriesPlaybackCompatibility(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	m := addMovie(t, st, "Big Buck Bunny", 2008)
	file := core.MediaFile{
		Path:    "Movies/Big Buck Bunny (2008)/Big Buck Bunny (2008).mkv",
		Size:    1 << 30,
		MovieID: m.ID,
		Quality: core.Quality1080p,
		Source:  core.SourceBluray,
		Codec:   "x265",
		Audio:   "DTS",
	}
	if err := st.UpsertMediaFile(ctx, &file); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/library/movies/"+itoa(m.ID), "")
	wantStatus(t, rec, http.StatusOK)
	var body movieJSON
	decodeBody(t, rec, &body)

	if body.File == nil {
		t.Fatalf("movie has no file DTO")
	}
	got := body.File.Compatibility
	if got.Verdict != core.TVCompatIncompatible {
		t.Fatalf("verdict = %q, want %q for HEVC/DTS/MKV on the safe profile", got.Verdict, core.TVCompatIncompatible)
	}
	joined := strings.Join(got.Reasons, "; ")
	for _, want := range []string{"HEVC video", "DTS audio", "MKV container"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("reasons = %q, want it to name %q", joined, want)
		}
	}

	// The capable target decodes HEVC in MKV, so only the DTS survives.
	assignMoviePlaybackTarget(t, st, m.ID, core.TVProfileCapable)
	rec = do(t, h, http.MethodGet, "/api/v1/library/movies/"+itoa(m.ID), "")
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &body)
	joined = strings.Join(body.File.Compatibility.Reasons, "; ")
	if body.File.Compatibility.Verdict != core.TVCompatIncompatible || !strings.Contains(joined, "DTS audio") {
		t.Fatalf("compatibility = %+v, want DTS still flagged on the capable profile", body.File.Compatibility)
	}
	if strings.Contains(joined, "HEVC") || strings.Contains(joined, "MKV") {
		t.Fatalf("reasons = %q, want HEVC and MKV cleared by the capable profile", joined)
	}
}
