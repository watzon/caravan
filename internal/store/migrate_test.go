package store

import (
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
