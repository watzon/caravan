package store

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migration is one numbered forward-only SQL migration.
type migration struct {
	version int
	name    string
	sql     string
}

// migrate applies every migration newer than the recorded schema version.
// Migrations are sequential and forward-only (SPEC §7): there is no down
// path, because the recovery story is "delete the database and rescan", not
// "roll back".
func (s *Store) migrate() error {
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("store: create schema_version: %w", err)
	}

	var current int
	// COALESCE covers the first run, where MAX over no rows is NULL.
	if err := s.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&current); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}

	migrations, err := loadMigrations(migrationFiles, "migrations")
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := s.applyMigration(m); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs one migration and records it, atomically: a failure
// leaves the database exactly as it was.
func (s *Store) applyMigration(m migration) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin migration %04d: %w", m.version, err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(m.sql); err != nil {
		return fmt.Errorf("store: apply migration %04d_%s: %w", m.version, m.name, err)
	}
	if _, err := tx.Exec(
		"INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)",
		m.version, m.name, formatTime(now()),
	); err != nil {
		return fmt.Errorf("store: record migration %04d: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit migration %04d: %w", m.version, err)
	}
	return nil
}

// loadMigrations reads and orders the migrations in dir. Filenames must be
// `NNNN_name.sql`; anything else is a build-time mistake and is reported
// rather than skipped, so a typo cannot silently drop a schema change.
func loadMigrations(fsys fs.FS, dir string) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("store: read migrations: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	seen := make(map[int]string, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, name, err := parseMigrationName(e.Name())
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("store: duplicate migration version %d (%s and %s)", version, prev, e.Name())
		}
		seen[version] = e.Name()

		body, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("store: read migration %s: %w", e.Name(), err)
		}
		migrations = append(migrations, migration{version: version, name: name, sql: string(body)})
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	return migrations, nil
}

// parseMigrationName splits `0001_init.sql` into (1, "init").
func parseMigrationName(filename string) (int, string, error) {
	base := strings.TrimSuffix(filename, ".sql")
	prefix, name, ok := strings.Cut(base, "_")
	if !ok || name == "" {
		return 0, "", fmt.Errorf("store: migration %q must be named NNNN_name.sql", filename)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil || version <= 0 {
		return 0, "", fmt.Errorf("store: migration %q has a non-numeric or zero version prefix", filename)
	}
	return version, name, nil
}

// SchemaVersion returns the highest applied migration version.
func (s *Store) SchemaVersion() (int, error) {
	var version int
	if err := s.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("store: read schema version: %w", err)
	}
	return version, nil
}
