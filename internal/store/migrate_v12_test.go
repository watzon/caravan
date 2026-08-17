package store

import (
	"testing"

	"github.com/watzon/caravan/internal/core"
	storemigrations "github.com/watzon/caravan/internal/store/migrations"
)

func TestMigrationTwelveAssignsDefaultSlugs(t *testing.T) {
	st, _ := openTemp(t)

	version, err := st.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if int64(version) != storemigrations.LatestVersion {
		t.Fatalf("schema version = %d, want %d", version, storemigrations.LatestVersion)
	}

	libs, err := st.ListLibraries(t.Context())
	if err != nil {
		t.Fatalf("ListLibraries: %v", err)
	}
	seen := map[string]bool{}
	for _, lib := range libs {
		if !core.ValidLibrarySlug(lib.Slug) {
			t.Errorf("library %d (%s) slug %q is not valid", lib.ID, lib.Name, lib.Slug)
		}
		if seen[lib.Slug] {
			t.Errorf("duplicate slug %q", lib.Slug)
		}
		seen[lib.Slug] = true
	}
	for _, slug := range []string{"movies", "series", "anime", "adult"} {
		if !seen[slug] {
			t.Errorf("missing default slug %q among %+v", slug, seen)
		}
	}
}
