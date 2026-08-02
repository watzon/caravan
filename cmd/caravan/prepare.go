package main

import (
	"errors"
	"os"

	"github.com/watzon/caravan/internal/prepare"
)

// runPrepare scaffolds a portable drive (SPEC §2.3).
//
// It is deliberately the one subcommand that touches no database and starts no
// server: preparing a drive is a filesystem operation, and a half-started
// Caravan would only get in the way of a drive that is about to be carried to
// another machine.
func runPrepare(args []string) error {
	fs := newFlagSet("prepare")
	force := fs.Bool("force", false,
		"refresh the launchers, README and binaries that are already on the drive "+
			"(never the config or the data folder)")
	binDir := fs.String("bin-dir", "",
		"directory holding release builds for the other operating systems "+
			"(default: next to this binary)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("prepare takes exactly one argument: the drive to prepare")
	}

	res, err := prepare.Run(prepare.Options{
		Target: fs.Arg(0),
		Force:  *force,
		BinDir: *binDir,
	})
	if err != nil {
		return err
	}
	res.Report(os.Stdout)
	return nil
}
