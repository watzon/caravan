//go:build !unix && !windows

package store

import "os"

func lockDatabaseInitFile(*os.File) error { return nil }
