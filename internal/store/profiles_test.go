package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestSetQualityProfileAssignments(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	profile := &core.QualityProfile{
		Name: "Transactional", Cutoff: core.Quality1080p, Items: []string{core.Quality1080p}, UpgradeAllowed: true,
	}
	if err := st.CreateQualityProfile(ctx, profile); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	movie := &core.Movie{TMDBID: 10_001, Title: "Movie"}
	if err := st.UpsertMovie(ctx, movie); err != nil {
		t.Fatalf("upsert movie: %v", err)
	}
	series := &core.Series{TMDBID: 10_002, Title: "Series"}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("upsert series: %v", err)
	}
	library, err := st.GetLibraryByKind(ctx, core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("get movie library: %v", err)
	}

	if err := st.SetMovieQualityProfile(ctx, movie.ID, profile.ID); err != nil {
		t.Fatalf("set movie profile: %v", err)
	}
	if err := st.SetSeriesQualityProfile(ctx, series.ID, profile.ID); err != nil {
		t.Fatalf("set series profile: %v", err)
	}
	if err := st.SetLibraryQualityProfile(ctx, library.ID, profile.ID); err != nil {
		t.Fatalf("set library profile: %v", err)
	}
	err = st.DeleteQualityProfile(ctx, profile.ID)
	var conflict *QualityProfileDeleteConflict
	if !errors.As(err, &conflict) ||
		conflict.References != (QualityProfileReferenceCounts{Libraries: 1, Movies: 1, Series: 1}) {
		t.Fatalf("delete assigned profile error = %v, want reference conflict for every target", err)
	}

	movie, err = st.GetMovie(ctx, movie.ID)
	if err != nil || movie.QualityProfileID != profile.ID {
		t.Fatalf("movie profile = %+v, %v; want %d", movie, err, profile.ID)
	}
	series, err = st.GetSeries(ctx, series.ID)
	if err != nil || series.QualityProfileID != profile.ID {
		t.Fatalf("series profile = %+v, %v; want %d", series, err, profile.ID)
	}
	library, err = st.GetLibrary(ctx, library.ID)
	if err != nil || library.QualityProfileID != profile.ID {
		t.Fatalf("library profile = %+v, %v; want %d", library, err, profile.ID)
	}

	if err := st.SetMovieQualityProfile(ctx, movie.ID, 0); err != nil {
		t.Fatalf("clear movie profile: %v", err)
	}
	if err := st.SetSeriesQualityProfile(ctx, series.ID, 0); err != nil {
		t.Fatalf("clear series profile: %v", err)
	}
	if err := st.SetLibraryQualityProfile(ctx, library.ID, 0); err != nil {
		t.Fatalf("clear library profile: %v", err)
	}

	movie, _ = st.GetMovie(ctx, movie.ID)
	series, _ = st.GetSeries(ctx, series.ID)
	library, _ = st.GetLibrary(ctx, library.ID)
	if movie.QualityProfileID != 0 || series.QualityProfileID != 0 || library.QualityProfileID != 0 {
		t.Fatalf("cleared profiles = movie %d, series %d, library %d; want zero",
			movie.QualityProfileID, series.QualityProfileID, library.QualityProfileID)
	}
	if err := st.SetMovieQualityProfile(ctx, movie.ID, profile.ID+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set unknown movie profile error = %v, want ErrNotFound", err)
	}
	movie, err = st.GetMovie(ctx, movie.ID)
	if err != nil || movie.QualityProfileID != 0 {
		t.Fatalf("movie after rejected assignment = %+v, %v; want zero profile", movie, err)
	}
}

func TestMovieQualityProfileAssignmentAndDeleteSerialization(t *testing.T) {
	tests := []struct {
		name        string
		deleteFirst bool
	}{
		{name: "deletion commits first", deleteFirst: true},
		{name: "assignment commits first"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			st, err := Open(filepath.Join(t.TempDir(), "caravan.db"))
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })

			profile := &core.QualityProfile{
				Name: "Racing", Cutoff: core.Quality1080p, Items: []string{core.Quality1080p}, UpgradeAllowed: true,
			}
			if err := st.CreateQualityProfile(ctx, profile); err != nil {
				t.Fatalf("create profile: %v", err)
			}
			movie := &core.Movie{TMDBID: 20_001, Title: "Race Movie"}
			if err := st.UpsertMovie(ctx, movie); err != nil {
				t.Fatalf("upsert movie: %v", err)
			}

			if tt.deleteFirst {
				if err := st.DeleteQualityProfile(ctx, profile.ID); err != nil {
					t.Fatalf("delete profile: %v", err)
				}
				if err := st.SetMovieQualityProfile(ctx, movie.ID, profile.ID); !errors.Is(err, ErrNotFound) {
					t.Fatalf("assign deleted profile error = %v, want ErrNotFound", err)
				}
				movie, err = st.GetMovie(ctx, movie.ID)
				if err != nil || movie.QualityProfileID != 0 {
					t.Fatalf("movie after rejected assignment = %+v, %v; want zero profile", movie, err)
				}
				return
			}

			if err := st.SetMovieQualityProfile(ctx, movie.ID, profile.ID); err != nil {
				t.Fatalf("assign profile: %v", err)
			}
			err = st.DeleteQualityProfile(ctx, profile.ID)
			var conflict *QualityProfileDeleteConflict
			if !errors.As(err, &conflict) || conflict.References.Movies != 1 {
				t.Fatalf("delete assigned profile error = %v, want movie reference conflict", err)
			}
			movie, err = st.GetMovie(ctx, movie.ID)
			if err != nil || movie.QualityProfileID != profile.ID {
				t.Fatalf("movie after rejected deletion = %+v, %v; want profile %d", movie, err, profile.ID)
			}
		})
	}
}

func TestDefaultQualityProfileAssignmentAndDeleteSerialization(t *testing.T) {
	tests := []struct {
		name        string
		deleteFirst bool
	}{
		{name: "deletion commits first", deleteFirst: true},
		{name: "default assignment commits first"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			st, err := Open(filepath.Join(t.TempDir(), "caravan.db"))
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })

			profile := &core.QualityProfile{
				Name: "Default Race", Cutoff: core.Quality1080p, Items: []string{core.Quality1080p}, UpgradeAllowed: true,
			}
			if err := st.CreateQualityProfile(ctx, profile); err != nil {
				t.Fatalf("create profile: %v", err)
			}

			if tt.deleteFirst {
				if err := st.DeleteQualityProfile(ctx, profile.ID); err != nil {
					t.Fatalf("delete profile: %v", err)
				}
				if err := st.SetDefaultQualityProfile(ctx, profile.ID); !errors.Is(err, ErrNotFound) {
					t.Fatalf("set deleted default error = %v, want ErrNotFound", err)
				}
				defaultProfile, err := st.GetDefaultQualityProfile(ctx)
				if err != nil || defaultProfile.ID == profile.ID {
					t.Fatalf("default after rejected assignment = %+v, %v; want a live profile", defaultProfile, err)
				}
				return
			}

			if err := st.SetDefaultQualityProfile(ctx, profile.ID); err != nil {
				t.Fatalf("set default profile: %v", err)
			}
			err = st.DeleteQualityProfile(ctx, profile.ID)
			var conflict *QualityProfileDeleteConflict
			if !errors.As(err, &conflict) || !conflict.Default {
				t.Fatalf("delete default profile error = %v, want default conflict", err)
			}
			defaultProfile, err := st.GetDefaultQualityProfile(ctx)
			if err != nil || defaultProfile.ID != profile.ID {
				t.Fatalf("default after rejected deletion = %+v, %v; want profile %d", defaultProfile, err, profile.ID)
			}
		})
	}
}

func TestGetQualityProfileReferenceCounts(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	profile := &core.QualityProfile{
		Name:           "Referenced",
		Cutoff:         core.Quality1080p,
		Items:          []string{core.Quality1080p},
		UpgradeAllowed: true,
	}
	if err := st.CreateQualityProfile(ctx, profile); err != nil {
		t.Fatalf("create quality profile: %v", err)
	}

	library, err := st.GetLibraryByKind(ctx, core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("get movie library: %v", err)
	}
	library.QualityProfileID = profile.ID
	if err := st.UpdateLibrary(ctx, library); err != nil {
		t.Fatalf("update movie library: %v", err)
	}
	if err := st.UpsertMovie(ctx, &core.Movie{
		TMDBID:           1001,
		Title:            "Referenced Movie",
		Monitored:        true,
		QualityProfileID: profile.ID,
	}); err != nil {
		t.Fatalf("upsert movie: %v", err)
	}
	if err := st.UpsertSeries(ctx, &core.Series{
		TMDBID:           1002,
		Title:            "Referenced Series",
		Monitored:        true,
		QualityProfileID: profile.ID,
	}); err != nil {
		t.Fatalf("upsert series: %v", err)
	}

	got, err := st.GetQualityProfileReferenceCounts(ctx, profile.ID)
	if err != nil {
		t.Fatalf("count references: %v", err)
	}
	want := QualityProfileReferenceCounts{Libraries: 1, Movies: 1, Series: 1}
	if got != want {
		t.Fatalf("reference counts = %+v, want %+v", got, want)
	}
}

func TestQualityProfileAcquisitionPolicyRoundTrips(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	profile := &core.QualityProfile{
		Name:                   "Policy",
		Cutoff:                 core.Quality1080p,
		Items:                  []string{core.Quality1080p},
		UpgradeAllowed:         true,
		PreferredSources:       []string{core.SourceWebDL, core.SourceBluray},
		ProperRepackPreference: core.ProperRepackPreferenceNeutral,
		MinSeeders:             12,
		MinSizeMB:              700,
		MaxSizeMB:              8_000,
		CustomFormats: []core.CustomFormat{
			{Name: "HDR", IncludeTerms: []string{"HDR"}, ExcludeTerms: []string{"DV"}, Score: 25},
		},
		TVProfile:             core.TVProfileCapable,
		TVCompatibilityPolicy: core.TVCompatibilityPolicyRequire,
	}
	if err := st.CreateQualityProfile(ctx, profile); err != nil {
		t.Fatalf("create quality profile: %v", err)
	}
	got, err := st.GetQualityProfile(ctx, profile.ID)
	if err != nil {
		t.Fatalf("get quality profile: %v", err)
	}
	if !reflect.DeepEqual(got.PreferredSources, profile.PreferredSources) ||
		got.ProperRepackPreference != profile.ProperRepackPreference ||
		got.MinSeeders != profile.MinSeeders ||
		got.MinSizeMB != profile.MinSizeMB ||
		got.MaxSizeMB != profile.MaxSizeMB ||
		!reflect.DeepEqual(got.CustomFormats, profile.CustomFormats) ||
		got.TVProfile != profile.TVProfile ||
		got.TVCompatibilityPolicy != profile.TVCompatibilityPolicy {
		t.Fatalf("stored policy = %+v, want %+v", got, profile)
	}

	profile.MinSeeders = 24
	profile.TVCompatibilityPolicy = core.TVCompatibilityPolicyPrefer
	if err := st.UpdateQualityProfile(ctx, profile); err != nil {
		t.Fatalf("update quality profile: %v", err)
	}
	got, err = st.GetQualityProfile(ctx, profile.ID)
	if err != nil {
		t.Fatalf("get updated quality profile: %v", err)
	}
	if got.MinSeeders != 24 || got.TVCompatibilityPolicy != core.TVCompatibilityPolicyPrefer {
		t.Fatalf("updated policy = %+v", got)
	}
}

func TestImportQualityProfilesUpsertsWithoutReplacingAssignments(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	existing := &core.QualityProfile{
		Name: "Existing", Cutoff: core.Quality1080p, Items: []string{core.Quality1080p}, UpgradeAllowed: true,
	}
	unlisted := &core.QualityProfile{
		Name: "Unlisted", Cutoff: core.Quality720p, Items: []string{core.Quality720p}, UpgradeAllowed: true,
	}
	for _, profile := range []*core.QualityProfile{existing, unlisted} {
		if err := st.CreateQualityProfile(ctx, profile); err != nil {
			t.Fatalf("create profile %q: %v", profile.Name, err)
		}
	}
	library, err := st.GetLibraryByKind(ctx, core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("get library: %v", err)
	}
	library.QualityProfileID = existing.ID
	if err := st.UpdateLibrary(ctx, library); err != nil {
		t.Fatalf("assign profile: %v", err)
	}

	if err := st.ImportQualityProfiles(ctx, []core.QualityProfile{
		{
			Name: "Existing", Cutoff: core.Quality2160p, Items: []string{core.Quality2160p},
			UpgradeAllowed: true, MinSeeders: 7,
		},
		{
			Name: "Imported", Cutoff: core.Quality1080p, Items: []string{core.Quality1080p},
			UpgradeAllowed: true,
		},
	}, "Imported"); err != nil {
		t.Fatalf("import profiles: %v", err)
	}
	updated, err := st.GetQualityProfile(ctx, existing.ID)
	if err != nil {
		t.Fatalf("get updated profile: %v", err)
	}
	if updated.Name != "Existing" || updated.MinSeeders != 7 || updated.Cutoff != core.Quality2160p {
		t.Fatalf("updated profile = %+v", updated)
	}
	if library, err = st.GetLibraryByKind(ctx, core.LibraryKindMovie); err != nil || library.QualityProfileID != existing.ID {
		t.Fatalf("library assignment = %+v, %v; want profile %d", library, err, existing.ID)
	}
	if _, err := st.GetQualityProfile(ctx, unlisted.ID); err != nil {
		t.Fatalf("unlisted profile was removed: %v", err)
	}
	defaultProfile, err := st.GetDefaultQualityProfile(ctx)
	if err != nil || defaultProfile.Name != "Imported" {
		t.Fatalf("default profile = %+v, %v; want Imported", defaultProfile, err)
	}
}
