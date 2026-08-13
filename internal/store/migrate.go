package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"strings"

	"github.com/pressly/goose/v3"
	storemigrations "github.com/watzon/caravan/internal/store/migrations"
)

const caravanApplicationID = 1129469518 // ASCII "CRVN"

// schemaFingerprints maps every supported public schema version to the
// canonical sqlite_master contract for its application-authored tables and
// indexes. Add the new fingerprint whenever a reviewed migration is added.
var schemaFingerprints = map[int]string{
	1: "bae95532d0be9ff024ada4952aa5b30a50ceb4d800fc83d9f91f9bcbdfee4f6e",
}

func (s *Store) migrate() error {
	ctx := context.Background()
	if err := runMigrations(ctx, s.db.DB, storemigrations.FS()); err != nil {
		return err
	}
	if err := validateCurrentSchema(ctx, s.db.DB); err != nil {
		return err
	}
	version, err := schemaVersion(ctx, s.db.DB)
	if err != nil {
		return err
	}
	if int64(version) != storemigrations.LatestVersion {
		return fmt.Errorf("store: migration stopped at version %d, want %d", version, storemigrations.LatestVersion)
	}
	return nil
}

func migrationProvider(db *sql.DB, migrations fs.FS) (*goose.Provider, error) {
	provider, err := goose.NewProvider(
		goose.DialectSQLite3,
		db,
		migrations,
		goose.WithTableName(storemigrations.VersionTable),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return nil, fmt.Errorf("store: initialize Goose migrations: %w", err)
	}
	return provider, nil
}

func runMigrations(ctx context.Context, db *sql.DB, migrations fs.FS) error {
	provider, err := migrationProvider(db, migrations)
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("store: apply Goose migrations: %w", err)
	}
	return nil
}

func validateSchemaIdentity(ctx context.Context, db *sql.DB, allowEmpty bool) error {
	var applicationID int
	if err := db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&applicationID); err != nil {
		return fmt.Errorf("store: read database identity: %w", err)
	}
	if applicationID == caravanApplicationID {
		version, err := schemaVersion(ctx, db)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrUnrecognizedSchema, err)
		}
		expected, ok := schemaFingerprints[version]
		if !ok {
			return fmt.Errorf("%w: no schema fingerprint for version %d", ErrUnrecognizedSchema, version)
		}
		fingerprint, err := schemaFingerprint(ctx, db)
		if err != nil {
			return err
		}
		if fingerprint != expected {
			return fmt.Errorf("%w: schema fingerprint %s", ErrUnrecognizedSchema, fingerprint)
		}
		return validateRequiredSeedContracts(ctx, db)
	}

	var legacyTables int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'schema_version'`,
	).Scan(&legacyTables); err != nil {
		return fmt.Errorf("store: inspect legacy database: %w", err)
	}
	if legacyTables != 0 {
		return ErrLegacySchema
	}

	var userTables int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite_%'
		  AND name != '`+storemigrations.VersionTable+`'`,
	).Scan(&userTables); err != nil {
		return fmt.Errorf("store: inspect database schema: %w", err)
	}
	if allowEmpty && userTables == 0 {
		return nil
	}
	return ErrUnrecognizedSchema
}

func schemaFingerprint(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT type, name, tbl_name, sql
		FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' AND type IN ('table', 'index')
		ORDER BY type, name`)
	if err != nil {
		return "", fmt.Errorf("store: read schema fingerprint: %w", err)
	}
	defer rows.Close()

	hash := sha256.New()
	for rows.Next() {
		var kind, name, table string
		var ddl sql.NullString
		if err := rows.Scan(&kind, &name, &table, &ddl); err != nil {
			return "", fmt.Errorf("store: read schema fingerprint: %w", err)
		}
		ddlText := strings.TrimSpace(ddl.String)
		fmt.Fprintf(hash, "%d:%s%d:%s%d:%s%d:%s\n",
			len(kind), kind, len(name), name, len(table), table, len(ddlText), ddlText)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("store: read schema fingerprint: %w", err)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func validateRequiredSeedContracts(ctx context.Context, db *sql.DB) error {
	checks := []struct {
		name  string
		query string
	}{
		{
			name: "default quality profile setting",
			query: `SELECT EXISTS (
				SELECT 1 FROM settings s
				JOIN quality_profiles qp ON qp.id = CAST(s.value AS INTEGER)
				WHERE s.key = 'default_quality_profile_id'
			)`,
		},
		{name: "default movie library", query: "SELECT EXISTS (SELECT 1 FROM libraries WHERE kind = 'movie' AND is_default = 1)"},
		{name: "default TV library", query: "SELECT EXISTS (SELECT 1 FROM libraries WHERE kind = 'tv' AND is_default = 1)"},
	}
	for _, check := range checks {
		var exists bool
		if err := db.QueryRowContext(ctx, check.query).Scan(&exists); err != nil {
			return fmt.Errorf("%w: validate %s: %v", ErrUnrecognizedSchema, check.name, err)
		}
		if !exists {
			return fmt.Errorf("%w: missing %s", ErrUnrecognizedSchema, check.name)
		}
	}
	return nil
}

func validateDatabaseBeforeOpen(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", readOnlySchemaDSN(path))
	if err != nil {
		return fmt.Errorf("store: open database for schema validation: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("store: read database for schema validation: %w", err)
	}
	return validateSchemaIdentity(ctx, db, true)
}

func preflightDatabaseIdentity(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", readOnlySchemaDSN(path))
	if err != nil {
		return fmt.Errorf("store: open database for identity preflight: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("store: read database for identity preflight: %w", err)
	}

	var applicationID int
	if err := db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&applicationID); err != nil {
		return fmt.Errorf("store: read database identity: %w", err)
	}
	if applicationID == caravanApplicationID {
		return validateCurrentSchema(ctx, db)
	}

	var legacyTables, otherTables int
	if err := db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN name = 'schema_version' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN name != 'caravan_schema_migrations' THEN 1 ELSE 0 END), 0)
		FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&legacyTables, &otherTables); err != nil {
		return fmt.Errorf("store: inspect database identity: %w", err)
	}
	if legacyTables != 0 {
		return ErrLegacySchema
	}
	if otherTables != 0 {
		return ErrUnrecognizedSchema
	}
	return nil
}

func readOnlySchemaDSN(path string) string {
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	return "file:" + path + "?" + q.Encode()
}

func validateCurrentSchema(ctx context.Context, db *sql.DB) error {
	return validateSchemaIdentity(ctx, db, false)
}

// SchemaVersion returns Caravan's current public migration version. The exact
// applied history is validated, so a newer, missing, or out-of-order history is
// rejected rather than opened by an incompatible binary.
func (s *Store) SchemaVersion() (int, error) {
	return schemaVersion(context.Background(), s.db.DB)
}

func schemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	return schemaVersionFor(ctx, db, storemigrations.FS())
}

func schemaVersionFor(ctx context.Context, db *sql.DB, migrations fs.FS) (int, error) {
	provider, err := migrationProvider(db, migrations)
	if err != nil {
		return 0, err
	}
	sources := provider.ListSources()
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT version_id, is_applied
		FROM %s
		ORDER BY id ASC`, storemigrations.VersionTable))
	if err != nil {
		return 0, fmt.Errorf("store: read schema migration history: %w", err)
	}
	defer rows.Close()

	rowIndex := 0
	current := int64(0)
	for rows.Next() {
		var version int64
		var applied bool
		if err := rows.Scan(&version, &applied); err != nil {
			return 0, fmt.Errorf("store: read schema migration history: %w", err)
		}
		if !applied {
			return 0, fmt.Errorf("store: migration history row %d for version %d is not applied", rowIndex, version)
		}
		if rowIndex == 0 {
			if version != 0 {
				return 0, fmt.Errorf("store: migration history must start with applied version 0, got %d", version)
			}
			rowIndex++
			continue
		}
		sourceIndex := rowIndex - 1
		if sourceIndex >= len(sources) {
			return 0, fmt.Errorf("store: migration history contains unknown version %d", version)
		}
		expected := sources[sourceIndex].Version
		if version != expected {
			return 0, fmt.Errorf("store: migration history row %d has version %d, want %d", rowIndex, version, expected)
		}
		current = version
		rowIndex++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: read schema migration history: %w", err)
	}
	if rowIndex == 0 {
		return 0, fmt.Errorf("store: migration history is missing applied version 0")
	}
	return int(current), nil
}

func isSchemaIdentityError(err error) bool {
	return errors.Is(err, ErrLegacySchema) || errors.Is(err, ErrUnrecognizedSchema) ||
		strings.Contains(err.Error(), "migration history") ||
		strings.Contains(err.Error(), "unknown or rolled-back migrations")
}
