// Package migrations owns Caravan's embedded Goose migration history.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed *.sql
var files embed.FS

// FS returns the complete migration filesystem rooted at this package.
func FS() fs.FS { return files }

const (
	// LatestVersion is Caravan's current public database version.
	LatestVersion int64 = 12
	// VersionTable is deliberately Caravan-specific so unrelated Goose users
	// cannot make another SQLite database look restorable here.
	VersionTable = "caravan_schema_migrations"
)
