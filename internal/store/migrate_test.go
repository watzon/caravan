package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestParseMigrationName(t *testing.T) {
	tests := []struct {
		filename    string
		wantVersion int
		wantName    string
		wantErr     bool
	}{
		{filename: "0001_init.sql", wantVersion: 1, wantName: "init"},
		{filename: "0012_add_jobs.sql", wantVersion: 12, wantName: "add_jobs"},
		{filename: "2_short.sql", wantVersion: 2, wantName: "short"},
		{filename: "init.sql", wantErr: true},
		{filename: "0001_.sql", wantErr: true},
		{filename: "abcd_init.sql", wantErr: true},
		{filename: "0000_zero.sql", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			version, name, err := parseMigrationName(tt.filename)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseMigrationName(%q) = (%d, %q, nil), want an error",
						tt.filename, version, name)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMigrationName(%q): %v", tt.filename, err)
			}
			if version != tt.wantVersion || name != tt.wantName {
				t.Errorf("parseMigrationName(%q) = (%d, %q), want (%d, %q)",
					tt.filename, version, name, tt.wantVersion, tt.wantName)
			}
		})
	}
}

// Migrations must apply in numeric, not lexical, order — the difference only
// shows up once there are ten of them.
func TestLoadMigrationsOrdersNumerically(t *testing.T) {
	fsys := fstest.MapFS{
		"m/0010_ten.sql":  {Data: []byte("SELECT 10;")},
		"m/0002_two.sql":  {Data: []byte("SELECT 2;")},
		"m/0001_one.sql":  {Data: []byte("SELECT 1;")},
		"m/notes.txt":     {Data: []byte("ignored")},
		"m/0003_three.md": {Data: []byte("ignored")},
	}

	migrations, err := loadMigrations(fsys, "m")
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}

	want := []int{1, 2, 10}
	if len(migrations) != len(want) {
		t.Fatalf("got %d migrations, want %d", len(migrations), len(want))
	}
	for i, version := range want {
		if migrations[i].version != version {
			t.Errorf("migrations[%d].version = %d, want %d", i, migrations[i].version, version)
		}
	}
}

func TestLoadMigrationsRejectsDuplicateVersions(t *testing.T) {
	fsys := fstest.MapFS{
		"m/0001_one.sql":     {Data: []byte("SELECT 1;")},
		"m/0001_one_bis.sql": {Data: []byte("SELECT 1;")},
	}

	if _, err := loadMigrations(fsys, "m"); err == nil {
		t.Error("loadMigrations with a duplicate version = nil error, want error")
	}
}

func TestLoadMigrationsRejectsBadFilename(t *testing.T) {
	fsys := fstest.MapFS{"m/init.sql": {Data: []byte("SELECT 1;")}}

	if _, err := loadMigrations(fsys, "m"); err == nil {
		t.Error("loadMigrations with an unnumbered file = nil error, want error")
	}
}

// The embedded set is what actually ships; a misnamed file there must fail
// loudly at startup rather than being skipped.
func TestEmbeddedMigrationsAreWellFormed(t *testing.T) {
	migrations, err := loadMigrations(migrationFiles, "migrations")
	if err != nil {
		t.Fatalf("loadMigrations(embedded): %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no embedded migrations found")
	}
	if migrations[0].version != 1 {
		t.Errorf("first embedded migration is version %d, want 1", migrations[0].version)
	}
	for _, m := range migrations {
		if m.sql == "" {
			t.Errorf("migration %04d_%s is empty", m.version, m.name)
		}
	}
}

// A prerelease build briefly put priority in 0001 as well as 0018. Databases
// created by that build stop at schema 17 with the final column already
// present. The real migration must accept that shape without discarding a
// priority the user has already set.
func TestMigration0018AddsOrAcceptsIndexerPriority(t *testing.T) {
	for _, tt := range []struct {
		name        string
		preexisting bool
		want        int
	}{
		{name: "adds missing column", want: 25},
		{name: "accepts prerelease column", preexisting: true, want: 7},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", dsn(filepath.Join(t.TempDir(), "caravan.db")))
			if err != nil {
				t.Fatalf("sql.Open: %v", err)
			}
			t.Cleanup(func() { db.Close() })
			st := &Store{db: db}

			if err := st.migrateTo(17); err != nil {
				t.Fatalf("migrateTo(17): %v", err)
			}
			if tt.preexisting {
				if _, err := db.Exec(
					"ALTER TABLE indexers ADD COLUMN priority INTEGER NOT NULL DEFAULT 25 CHECK (priority >= 0)",
				); err != nil {
					t.Fatalf("add prerelease priority column: %v", err)
				}
			}

			columns := "name, protocol, url, api_key, categories, enabled, created_at, updated_at"
			values := "'legacy', 'torznab', 'https://indexer.test', '', '', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'"
			if tt.preexisting {
				columns += ", priority"
				values += ", 7"
			}
			if _, err := db.Exec("INSERT INTO indexers (" + columns + ") VALUES (" + values + ")"); err != nil {
				t.Fatalf("insert indexer: %v", err)
			}

			if err := st.migrateTo(18); err != nil {
				t.Fatalf("migrateTo(18): %v", err)
			}

			var got, priorityColumns, migrationRows int
			if err := db.QueryRow("SELECT priority FROM indexers WHERE name = 'legacy'").Scan(&got); err != nil {
				t.Fatalf("read priority: %v", err)
			}
			if err := db.QueryRow(
				"SELECT COUNT(*) FROM pragma_table_info('indexers') WHERE name = 'priority'",
			).Scan(&priorityColumns); err != nil {
				t.Fatalf("count priority columns: %v", err)
			}
			if err := db.QueryRow(
				"SELECT COUNT(*) FROM schema_version WHERE version = 18 AND name = 'indexer_priority'",
			).Scan(&migrationRows); err != nil {
				t.Fatalf("read schema version: %v", err)
			}
			if got != tt.want {
				t.Errorf("priority = %d, want %d", got, tt.want)
			}
			if priorityColumns != 1 {
				t.Errorf("priority columns = %d, want 1", priorityColumns)
			}
			if migrationRows != 1 {
				t.Errorf("migration rows = %d, want 1", migrationRows)
			}
		})
	}
}

// Some prerelease databases recorded 0017 before every acquisition-policy
// column had landed in that file. A later migration must reconcile every final
// column, preserve any policy already stored, and also accept a complete 0017.
func TestMigration0020ReconcilesAcquisitionProfileColumns(t *testing.T) {
	type policy struct {
		preferredSources string
		properRepack     string
		minSeeders       int
		minSizeMB        int
		maxSizeMB        int
		customFormats    string
		tvProfile        string
		tvPolicy         string
	}

	defaults := policy{"[]", "prefer", 0, 0, 0, "[]", "safe", "ignore"}
	custom := policy{`["bluray"]`, "require", 5, 100, 40000, `[{"name":"HDR","score":3}]`, "compatible", "require"}

	for _, tt := range []struct {
		name  string
		setup func(*testing.T, *sql.DB, *Store)
		want  policy
	}{
		{
			name: "adds every column omitted by an early 0017",
			setup: func(t *testing.T, db *sql.DB, st *Store) {
				t.Helper()
				if err := st.migrateTo(16); err != nil {
					t.Fatalf("migrateTo(16): %v", err)
				}
				recordAppliedMigration(t, db, 17, "acquisition_profile_policy")
			},
			want: defaults,
		},
		{
			name: "adds later columns and preserves earlier policy",
			setup: func(t *testing.T, db *sql.DB, st *Store) {
				t.Helper()
				if err := st.migrateTo(16); err != nil {
					t.Fatalf("migrateTo(16): %v", err)
				}
				if _, err := db.Exec(`
					ALTER TABLE quality_profiles ADD COLUMN preferred_sources TEXT NOT NULL DEFAULT '[]';
					ALTER TABLE quality_profiles ADD COLUMN proper_repack_preference TEXT NOT NULL DEFAULT 'prefer';
					ALTER TABLE quality_profiles ADD COLUMN min_seeders INTEGER NOT NULL DEFAULT 0;
					ALTER TABLE quality_profiles ADD COLUMN min_size_mb INTEGER NOT NULL DEFAULT 0;
					ALTER TABLE quality_profiles ADD COLUMN max_size_mb INTEGER NOT NULL DEFAULT 0;
					ALTER TABLE quality_profiles ADD COLUMN custom_formats TEXT NOT NULL DEFAULT '[]';
					UPDATE quality_profiles
					SET preferred_sources = '["bluray"]',
					    proper_repack_preference = 'require',
					    min_seeders = 5,
					    min_size_mb = 100,
					    max_size_mb = 40000,
					    custom_formats = '[{"name":"HDR","score":3}]';
				`); err != nil {
					t.Fatalf("create partial prerelease policy: %v", err)
				}
				recordAppliedMigration(t, db, 17, "acquisition_profile_policy")
			},
			want: policy{`["bluray"]`, "require", 5, 100, 40000, `[{"name":"HDR","score":3}]`, "safe", "ignore"},
		},
		{
			name: "accepts and preserves a complete prerelease policy",
			setup: func(t *testing.T, db *sql.DB, st *Store) {
				t.Helper()
				if err := st.migrateTo(19); err != nil {
					t.Fatalf("migrateTo(19): %v", err)
				}
				if _, err := db.Exec(`
					UPDATE quality_profiles
					SET preferred_sources = ?, proper_repack_preference = ?,
					    min_seeders = ?, min_size_mb = ?, max_size_mb = ?,
					    custom_formats = ?, tv_profile = ?,
					    tv_compatibility_policy = ?`,
					custom.preferredSources, custom.properRepack, custom.minSeeders,
					custom.minSizeMB, custom.maxSizeMB, custom.customFormats,
					custom.tvProfile, custom.tvPolicy,
				); err != nil {
					t.Fatalf("store complete prerelease policy: %v", err)
				}
			},
			want: custom,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", dsn(filepath.Join(t.TempDir(), "caravan.db")))
			if err != nil {
				t.Fatalf("sql.Open: %v", err)
			}
			t.Cleanup(func() { db.Close() })
			st := &Store{db: db}
			tt.setup(t, db, st)

			if err := st.migrateTo(20); err != nil {
				t.Fatalf("migrateTo(20): %v", err)
			}

			var got policy
			if err := db.QueryRow(`
				SELECT preferred_sources, proper_repack_preference,
				       min_seeders, min_size_mb, max_size_mb, custom_formats,
				       tv_profile, tv_compatibility_policy
				FROM quality_profiles
				WHERE name = ?`,
				DefaultQualityProfileName,
			).Scan(
				&got.preferredSources, &got.properRepack, &got.minSeeders,
				&got.minSizeMB, &got.maxSizeMB, &got.customFormats,
				&got.tvProfile, &got.tvPolicy,
			); err != nil {
				t.Fatalf("read reconciled policy: %v", err)
			}
			if got != tt.want {
				t.Errorf("policy = %+v, want %+v", got, tt.want)
			}

			var policyColumns, migrationRows int
			if err := db.QueryRow(`
				SELECT COUNT(*) FROM pragma_table_info('quality_profiles')
				WHERE name IN (
					'preferred_sources', 'proper_repack_preference',
					'min_seeders', 'min_size_mb', 'max_size_mb',
					'custom_formats', 'tv_profile', 'tv_compatibility_policy'
				)`,
			).Scan(&policyColumns); err != nil {
				t.Fatalf("count policy columns: %v", err)
			}
			if err := db.QueryRow(
				"SELECT COUNT(*) FROM schema_version WHERE version = 20 AND name = 'profile_policy_repair'",
			).Scan(&migrationRows); err != nil {
				t.Fatalf("read schema version: %v", err)
			}
			if policyColumns != 8 {
				t.Errorf("policy columns = %d, want 8", policyColumns)
			}
			if migrationRows != 1 {
				t.Errorf("migration rows = %d, want 1", migrationRows)
			}
		})
	}
}

func recordAppliedMigration(t *testing.T, db *sql.DB, version int, name string) {
	t.Helper()
	if _, err := db.Exec(
		"INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)",
		version, name, formatTime(now()),
	); err != nil {
		t.Fatalf("record migration %04d_%s: %v", version, name, err)
	}
}
