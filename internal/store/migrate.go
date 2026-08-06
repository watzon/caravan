package store

import (
	"database/sql"
	"embed"
	"errors"
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
func (s *Store) migrate() error { return s.migrateTo(allMigrations) }

// allMigrations is migrateTo's "no ceiling" target.
const allMigrations = -1

// migrateTo applies every pending migration up to and including target. Only
// migrate calls it with allMigrations; the tests use a real ceiling to build a
// database at an older schema version and watch a later migration run against
// it, which is the only way to prove an upgrade in place.
func (s *Store) migrateTo(target int) error {
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
		if target != allMigrations && m.version > target {
			break
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

	handled, err := applyCompatibilityMigration(tx, m)
	if err != nil {
		return fmt.Errorf("store: prepare migration %04d_%s: %w", m.version, m.name, err)
	}
	if !handled {
		if _, err := tx.Exec(m.sql); err != nil {
			return fmt.Errorf("store: apply migration %04d_%s: %w", m.version, m.name, err)
		}
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

// migrationColumn is one final column definition a compatibility migration
// can validate or add. The SQL is static repository data, never user input.
type migrationColumn struct {
	table        string
	name         string
	columnType   string
	defaultValue string
	addSQL       string
}

var (
	indexerPriorityColumn = migrationColumn{
		table:        "indexers",
		name:         "priority",
		columnType:   "INTEGER",
		defaultValue: "25",
		addSQL:       "ALTER TABLE indexers ADD COLUMN priority INTEGER NOT NULL DEFAULT 25 CHECK (priority >= 0)",
	}
	profilePolicyColumns = [...]migrationColumn{
		{"quality_profiles", "preferred_sources", "TEXT", "'[]'", "ALTER TABLE quality_profiles ADD COLUMN preferred_sources TEXT NOT NULL DEFAULT '[]'"},
		{"quality_profiles", "proper_repack_preference", "TEXT", "'prefer'", "ALTER TABLE quality_profiles ADD COLUMN proper_repack_preference TEXT NOT NULL DEFAULT 'prefer'"},
		{"quality_profiles", "min_seeders", "INTEGER", "0", "ALTER TABLE quality_profiles ADD COLUMN min_seeders INTEGER NOT NULL DEFAULT 0"},
		{"quality_profiles", "min_size_mb", "INTEGER", "0", "ALTER TABLE quality_profiles ADD COLUMN min_size_mb INTEGER NOT NULL DEFAULT 0"},
		{"quality_profiles", "max_size_mb", "INTEGER", "0", "ALTER TABLE quality_profiles ADD COLUMN max_size_mb INTEGER NOT NULL DEFAULT 0"},
		{"quality_profiles", "custom_formats", "TEXT", "'[]'", "ALTER TABLE quality_profiles ADD COLUMN custom_formats TEXT NOT NULL DEFAULT '[]'"},
		{"quality_profiles", "tv_profile", "TEXT", "'safe'", "ALTER TABLE quality_profiles ADD COLUMN tv_profile TEXT NOT NULL DEFAULT 'safe'"},
		{"quality_profiles", "tv_compatibility_policy", "TEXT", "'ignore'", "ALTER TABLE quality_profiles ADD COLUMN tv_compatibility_policy TEXT NOT NULL DEFAULT 'ignore'"},
	}
)

// applyCompatibilityMigration handles two schema shapes written by prerelease
// builds. SQLite has no ADD COLUMN IF NOT EXISTS, so these upgrades must first
// validate any existing column and add only those that are absent.
func applyCompatibilityMigration(tx *sql.Tx, m migration) (bool, error) {
	switch {
	case m.version == 18 && m.name == "indexer_priority":
		present, err := migrationColumnPresent(tx, indexerPriorityColumn)
		return present, err
	case m.version == 20 && m.name == "profile_policy_repair":
		for _, column := range profilePolicyColumns {
			present, err := migrationColumnPresent(tx, column)
			if err != nil {
				return false, err
			}
			if present {
				continue
			}
			if _, err := tx.Exec(column.addSQL); err != nil {
				return false, fmt.Errorf("add %s.%s: %w", column.table, column.name, err)
			}
		}
		return true, nil
	default:
		return false, nil
	}
}

func migrationColumnPresent(tx *sql.Tx, column migrationColumn) (bool, error) {
	var columnType string
	var notNull int
	var defaultValue sql.NullString
	err := tx.QueryRow(
		`SELECT type, [notnull], dflt_value
		 FROM pragma_table_info(?)
		 WHERE name = ?`,
		column.table, column.name,
	).Scan(&columnType, &notNull, &defaultValue)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(columnType, column.columnType) || notNull != 1 ||
		!defaultValue.Valid || defaultValue.String != column.defaultValue {
		return false, fmt.Errorf(
			"existing %s.%s column has an incompatible definition",
			column.table, column.name,
		)
	}
	return true, nil
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
