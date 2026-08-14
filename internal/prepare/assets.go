package prepare

import (
	"fmt"
	"path"
	"strings"
)

// portableURL is where the launchers send the browser. It is the URL form of
// config.DefaultPortableListen, which portable mode binds when the config file
// names no address; assets_test.go pins the two together so a change to one
// cannot silently leave the launchers opening a dead page.
const portableURL = "http://127.0.0.1:8677"

// configYAML is the default drive bootstrap config retained for asset tests.
var configYAML = portableConfigYAML(DataDir, StorageRoot)

// portableConfigYAML builds the drive's bootstrap config from the locations
// selected during preparation.
//
// It deliberately sets no listen address: portable mode already defaults to
// loopback (SPEC §11), and repeating the address here would be a second place
// to change it.
func portableConfigYAML(dataDir, storageRoot string) string {
	return fmt.Sprintf(`# Caravan portable drive configuration (SPEC §2.3).
#
# Every path in this file is RELATIVE, and that is the whole trick behind a
# portable drive. The launchers at the drive root change directory to their own
# folder before starting Caravan, so "." means *this drive* wherever the
# computer decided to mount it: /Volumes/CARAVAN on a Mac, /media/you/CARAVAN
# on Linux, D:\ or E:\ on Windows. Nothing has to be rewritten when the drive
# letter or mount point changes, and the library keeps working on every machine
# the drive is plugged into.
#
# Do not replace these with absolute paths. An absolute path pins the drive to
# one computer.

# Portable behaviour: seeding starts paused, the clean-shutdown marker is
# checked on every start, and the UI offers "Shut down safely" before ejecting.
portable: true

# The storage root selected during prepare. library/ and incomplete/ live under it,
# and every path Caravan stores in its database is relative to it.
storage_root: %q

# Where Caravan keeps its own state: caravan.db, the clean-shutdown marker and
# restore staging. The database is a rebuildable cache -- delete it and rescan
# and the library comes back -- but the marker is not, so do not delete this
# folder while Caravan is running.
data_dir: %q

# Portable mode binds 127.0.0.1 by default, because this drive travels onto
# coffee-shop and hotel networks. Set a password under Settings > Security
# before listening on anything wider.

log_level: "info"
`, storageRoot, dataDir)
}

// macLauncher is double-clicked in Finder.
//
// The chdir is load-bearing: a .command opened from Finder starts in the user's
// home directory, so without it every relative path in caravan.yaml would
// resolve against the wrong disk entirely.
const macLauncher = `#!/bin/sh
# Caravan portable launcher (macOS).
#
# Resolves its own directory -- the drive root -- and starts the matching
# binary from there. Every path in caravan/caravan.yaml is relative to this
# directory, so the drive works at whatever mount point macOS gives it.
set -e
cd "$(dirname "$0")"

case "$(uname -m)" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64)  arch=amd64 ;;
  *) echo "Caravan: unsupported CPU $(uname -m)" >&2; exit 1 ;;
esac

bin="caravan/bin/darwin-$arch/caravan"
if [ ! -f "$bin" ]; then
  echo "Caravan: $bin is missing." >&2
  echo "Run 'caravan prepare' against this drive from a Mac, or copy the" >&2
  echo "darwin-$arch build of caravan into that folder." >&2
  exit 1
fi
# exFAT carries no permission bits, so this is usually a no-op; it matters on a
# drive formatted APFS or HFS+, where the copy may have arrived without it.
chmod +x "$bin" 2>/dev/null || true

# Opened in the background so the browser arrives after the server is listening.
( sleep 2; open "` + portableURL + `" ) &

echo "Caravan is starting. The web UI is at ` + portableURL + `"
echo "Use 'Shut down safely' in the UI before unplugging this drive."
exec "$bin" serve -config caravan/caravan.yaml
`

// linuxLauncher is run from a terminal or a file manager.
const linuxLauncher = `#!/bin/sh
# Caravan portable launcher (Linux).
#
# Resolves its own directory -- the drive root -- and starts the matching
# binary from there. Every path in caravan/caravan.yaml is relative to this
# directory, so the drive works at whatever mount point the desktop gives it.
set -e
self="$0"
# readlink -f is coreutils; the fallback keeps this working on busybox.
if command -v readlink >/dev/null 2>&1 && readlink -f "$self" >/dev/null 2>&1; then
  self="$(readlink -f "$self")"
fi
cd "$(dirname "$self")"

case "$(uname -m)" in
  x86_64|amd64)  arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "Caravan: unsupported CPU $(uname -m)" >&2; exit 1 ;;
esac

bin="caravan/bin/linux-$arch/caravan"
if [ ! -f "$bin" ]; then
  echo "Caravan: $bin is missing." >&2
  echo "Run 'caravan prepare' against this drive from Linux, or copy the" >&2
  echo "linux-$arch build of caravan into that folder." >&2
  exit 1
fi
# exFAT carries no permission bits, so this is usually a no-op; it matters on a
# drive formatted ext4, where the copy may have arrived without it.
chmod +x "$bin" 2>/dev/null || true

if command -v xdg-open >/dev/null 2>&1; then
  ( sleep 2; xdg-open "` + portableURL + `" >/dev/null 2>&1 ) &
fi

echo "Caravan is starting. The web UI is at ` + portableURL + `"
echo "Use 'Shut down safely' in the UI before unplugging this drive."
exec "$bin" serve -config caravan/caravan.yaml
`

// windowsLauncherLF is the .bat source; windowsLauncher is the CRLF form
// actually written, because cmd.exe is the one shell here that still cares.
const windowsLauncherLF = `@echo off
rem Caravan portable launcher (Windows).
rem
rem %~dp0 is this file's own folder -- the drive root -- so the launcher works
rem from whatever letter Windows assigned the drive. Every path in
rem caravan\caravan.yaml is relative to it.
setlocal
cd /d "%~dp0"

set "BIN=caravan\bin\windows-amd64\caravan.exe"
if not exist "%BIN%" (
  echo Caravan: %BIN% is missing.
  echo Run "caravan prepare" against this drive from Windows, or copy the
  echo windows-amd64 build of caravan.exe into that folder.
  pause
  exit /b 1
)

rem Opened in the background so the browser arrives after the server is
rem listening. ping is the sleep every Windows has.
start "" /b cmd /c "ping -n 4 127.0.0.1 >nul & start ` + portableURL + `"

echo Caravan is starting. The web UI is at ` + portableURL + `
echo Use "Shut down safely" in the UI before unplugging this drive.
"%BIN%" serve -config caravan\caravan.yaml

echo.
echo Caravan has stopped. It is now safe to eject the drive.
pause
`

// windowsLauncher is windowsLauncherLF with CRLF line endings.
var windowsLauncher = strings.ReplaceAll(windowsLauncherLF, "\n", "\r\n")

// readme is the default-layout README retained for asset tests.
var readme = portableReadme(DataDir, StorageRoot)

// portableReadme describes the actual locations selected during preparation;
// it is the only documentation somebody who finds the drive in a drawer has.
func portableReadme(dataDir, storageRoot string) string {
	dir := func(value string) string {
		if value == "." {
			return "./"
		}
		return value + "/"
	}
	libraryDir := path.Join(storageRoot, "library")
	return strings.NewReplacer(
		"{{PORTABLE_URL}}", portableURL,
		"{{LIBRARY_DIR}}", dir(libraryDir),
		"{{MOVIES_DIR}}", dir(path.Join(libraryDir, "Movies")),
		"{{TV_DIR}}", dir(path.Join(libraryDir, "TV")),
		"{{INCOMPLETE_DIR}}", dir(path.Join(storageRoot, "incomplete")),
		"{{DATA_DIR}}", dir(dataDir),
		"{{DATABASE_PATH}}", path.Join(dataDir, "caravan.db"),
	).Replace(readmeTemplate)
}

const readmeTemplate = `Caravan portable drive
======================

This drive carries a whole media library and the software that manages it.
Plug it into a computer and run the launcher for that operating system; plug it
into a television's USB port and browse {{LIBRARY_DIR}} as an ordinary media drive.
No server runs in TV mode -- that is the point.


Starting it
-----------

  macOS    double-click Start-Mac.command
  Windows  double-click Start-Windows.bat
  Linux    run ./Start-Linux.sh (or double-click it, if your file manager is
           set to run executable text files)

Each launcher works out which folder it is in, picks the binary for the current
operating system and CPU, and opens {{PORTABLE_URL}} in your browser.


Windows: the first-run SmartScreen warning
------------------------------------------

Caravan's binaries are not code-signed, so the first time you run
Start-Windows.bat Windows will show a blue "Windows protected your PC" box and
refuse to start it.

  1. Click "More info".
  2. Click "Run anyway".

Windows remembers the choice for that copy of the file. If you would rather
check first, the file came from wherever you downloaded Caravan; nothing on
this drive phones home during startup.

Some antivirus products also flag unsigned binaries that open a network port.
Caravan listens on 127.0.0.1 only -- your own machine -- unless you change the
listen address in caravan\caravan.yaml.


Unplugging it safely
--------------------

The drive is almost certainly formatted exFAT, and exFAT has no journal: pulling
it out while Caravan is writing can corrupt the library database.

  1. In the Caravan UI, use "Shut down safely".
  2. Wait for the page that says the drive is safe to eject.
  3. Eject the drive from your desktop, then unplug it.

If the drive is ever pulled without that, Caravan notices on the next start and
offers to check the database and rescan the library before anything else runs.
It will also print the filesystem-check command for your operating system --
Caravan never runs one itself.


What is on here
---------------

  {{MOVIES_DIR}}  your films, in folders a television can browse
  {{TV_DIR}}  your series, likewise
  {{INCOMPLETE_DIR}}  downloads still in progress; safe to delete when idle
  caravan/caravan.yaml  configuration, all paths relative to this drive
  {{DATA_DIR}}  the database, restore state and clean-shutdown marker
  caravan/bin/          one Caravan binary per operating system

Deleting {{DATABASE_PATH}} is not fatal: start Caravan again, rescan, and
the library comes back from the files themselves. Do not delete it while
Caravan is running.


Filesystem notes
----------------

exFAT is the only filesystem that reads and writes on Windows, macOS and Linux
alike, and most televisions read it. Drives larger than 2 TiB must be
GPT-partitioned; some older televisions only read MBR, so check the TV's manual.

Caravan never formats or repartitions a drive. If this one needs reformatting,
do it yourself -- that erases everything on it -- and run "caravan prepare"
against it again afterwards.
`
