// Command caravan is the Caravan server: one binary, one database, one
// storage root (SPEC §1.2).
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

// version is the build version, overridden at release time with
// -ldflags "-X main.version=v1.2.3".
var version = "dev"

const usage = `caravan - self-hosted media manager

Usage:
  caravan serve [flags]         Run the server
  caravan prepare <drive>       Scaffold a portable drive
  caravan version               Print the version

Run "caravan <command> -h" for the flags of a command.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "caravan:", err)
		os.Exit(1)
	}
}

// run dispatches a subcommand. It returns errors instead of exiting so the
// whole command surface stays testable.
func run(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return errors.New("no command given")
	}

	switch args[0] {
	case "serve":
		return runServe(args[1:])
	case "prepare":
		return runPrepare(args[1:])
	case "version":
		fmt.Println("caravan", version)
		return nil
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// newFlagSet returns a flag set that reports errors to the caller rather than
// calling os.Exit, so run stays in control of the exit path.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}
