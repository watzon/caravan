# The portable drive

`caravan prepare /path/to/drive` turns an ordinary external drive into a
self-contained Caravan install: the binaries for every supported OS, a launcher
per OS, a drive-relative config, and the library layout a television can browse
over USB with no server running at all.

This page is the operator's half of that. The command itself is documented by
`caravan prepare -h`; what follows is the layout it produces, the rules that
make the drive portable, and — at the end — the manual verification matrix that
closes out phase 5's acceptance criterion, because none of it can be proven in
CI.

---

## Layout

```
DRIVE/
  Start-Mac.command          double-clicked in Finder
  Start-Windows.bat          double-clicked in Explorer
  Start-Linux.sh             run from a terminal or a file manager
  README.txt                 the only docs somebody who finds this drive will have
  caravan/
    caravan.yaml             bootstrap config; storage_root is "."
    bin/
      darwin-amd64/caravan
      darwin-arm64/caravan
      linux-amd64/caravan
      linux-arm64/caravan
      windows-amd64/caravan.exe
  library/                   the media, in the layout a TV's USB browser reads
  incomplete/                in-flight downloads
```

`library/` and `incomplete/` sit at the drive root rather than under `caravan/`
on purpose: a television plugged into the USB port shows the drive's top level,
and the first thing on it should be the media.

### Why the config says `storage_root: "."`

Every path Caravan stores in its database is relative to the storage root, and
on a portable drive the root is the drive itself. `"."` resolves against the
launcher's own directory, so the same drive works at `/Volumes/CARAVAN` on a
Mac, `D:\` on Windows and `/media/you/CARAVAN` on Linux without a single write.

An absolute path in that file — or in the settings table — pins the drive to
one machine and it stops working on the next one. That is why **Settings →
Storage disables re-point and migrate in portable mode**: both operations
require absolute paths, and honouring them would quietly break the drive. To
move a portable library onto a server, copy `library/` across and rescan there.

### Filesystem

Format the drive **exFAT on a GPT partition table**. `prepare` warns when it
can tell you have not:

- **exFAT** is the only filesystem macOS, Windows and Linux all read *and*
  write without extra drivers, and the only one televisions reliably mount.
  NTFS is read-only on stock macOS; HFS+/APFS is unreadable on Windows;
  ext4 is unreadable on both.
- **GPT** because MBR caps a partition at 2 TB, and the drives this feature is
  for are larger than that.

exFAT has no hardlinks, so imports on a portable drive copy rather than link.
That is expected: a completed download occupies its bytes twice until seeding
finishes and the incomplete copy is removed.

---

## Ejecting safely

exFAT has no journal. A drive pulled mid-write can lose the last writes, and
sqlite's write-ahead log is exactly where those writes live. So:

1. **Sidebar → Shut down safely.** The server drains HTTP, stops the import
   watcher, sends the DLNA byebye, flushes every download's resume data through
   sqlite, runs `PRAGMA wal_checkpoint(TRUNCATE)`, closes the database, and
   writes the fsync'd clean-shutdown marker.
2. **Wait for the "Safe to eject" screen.** It does not appear when the server
   accepts the request — it appears when the server has stopped answering at
   all, which is the only observable proof the teardown finished. If it says
   *"Caravan is still stopping"* instead, the process was still up after two
   minutes: wait for its window or terminal to close before unplugging.
3. Eject from your desktop as usual, then unplug.

If the drive is pulled without that, the next start reads the marker as
`running`, reports a dirty start, refuses to resume downloads, and offers
**Verify & rescan**: `PRAGMA integrity_check` plus a library rescan. Caravan
never runs `fsck` itself — the filesystem has to be unmounted for that, and a
repair tool running from the drive it is repairing is its own hazard. The
recovery banner prints the per-OS command instead.

Only one Caravan may own a drive at a time. A second launch (the usual cause is
double-clicking the launcher when the first terminal window went unnoticed)
refuses to start rather than opening the same database twice.

---

## Verification status

**Not yet verified end to end.** The acceptance criterion —

> `prepare` on a real exFAT drive produces a layout that launches via the
> click-launcher on at least two OSes, and the drive's library plays in a TV's
> USB browser

— needs physical hardware and cannot be checked in CI. PLAN.md's risk register
says as much: *"exFAT integrity can't be fully simulated in CI; phase 5 needs a
manual test matrix with a physical drive and at least one real TV."* This is
that matrix.

### What is already verified automatically

- `cmd/caravan/prepare_test.go` — the scaffold, the drive-relative config
  (asserting the running storage root is exactly `"."`), and re-running
  `prepare` on a drive somebody already carries.
- `internal/prepare` — launcher arch detection, CRLF on the `.bat`, every slot
  a launcher can execute has a release target, and the offline `-bin-dir` path.
- `internal/integrity` — clean/dirty marker lifecycle, including the
  second-instance case.
- `.github/workflows/ci.yml` cross-compiles all five targets on every push, so
  a binary the launchers ask for always exists to copy.

### Equipment

| Item | Requirement |
|---|---|
| Drive | USB 3.0 or better, ≥ 256 GB, **exFAT on GPT**. A spinning disk is the honest test; an SSD hides seek costs the TV browser will not. |
| Machine A | Apple Silicon Mac (`darwin/arm64`) |
| Machine B | Windows 11 x64 (`windows/amd64`) |
| Machine C *(optional but closes the Intel gap)* | Intel Mac (`darwin/amd64`) or an x64 Linux box |
| Television | Any smart TV with a USB port and a built-in media browser. Record make, model and firmware — DLNA and USB browser behaviour vary per vendor and the result is only meaningful with the model named. |
| Release artifacts | An unpacked release directory containing all five builds, for `caravan prepare -bin-dir`. |

### Procedure

Record a pass/fail and the exact hardware for every step.

**1. Format and prepare.**

```sh
# macOS: erase as ExFAT with a GUID Partition Map (Disk Utility, or:)
diskutil eraseDisk ExFAT CARAVAN GPT /dev/diskN

caravan prepare /Volumes/CARAVAN -bin-dir ~/caravan-release
```

Pass: the report lists all five `bin/` slots as placed, warns about nothing, and
does **not** print a filesystem or partition-table warning.

**2. Wrong-filesystem warning (negative test).** Repeat step 1 against an
HFS+/APFS or NTFS volume. Pass: `prepare` still succeeds but prints the exFAT
warning. This is the check that stops somebody carrying a drive Windows cannot
write to.

**3. Machine A — first launch.** Plug into the Mac, double-click
`Start-Mac.command`. Pass: a terminal window opens, the browser lands on
`http://127.0.0.1:8677`, and the UI shows **mode: portable**. No first-run
screen — the drive brought its own root.

**4. Import a library.** Add two or three movies and one series. Pass: files
land under `library/` in the `Movies/Title (Year)/…` and `TV/Show/Season 01/…`
layout, and Settings → Storage shows `.` as the current root.

**5. Safe eject on machine A.** Sidebar → Shut down safely. Pass: the screen
says **Safe to eject** (not "still stopping"), and `caravan/caravan.state`
contains `clean`. Eject and unplug.

**6. Machine B — second OS.** Plug into Windows, double-click
`Start-Windows.bat`. Accept the SmartScreen prompt once ("More info" → "Run
anyway"). Pass: the UI comes up, the library from step 4 is **already there
with no rescan**, and every poster renders. This is the criterion's *"launches
via the click-launcher on at least two OSes"* — a library that reappears
without a rescan is the proof the paths really were relative.

**7. Machine C — the Intel/Linux slot.** Repeat step 6 on machine C. Pass: same
result, from `bin/darwin-amd64/` or `bin/linux-amd64/`.

**8. Dirty eject and recovery.** On any machine, start Caravan, begin a
download, and **pull the drive without shutting down** (or `kill -9` the
process and unplug). Plug it back in and start Caravan. Pass:

- the UI shows the *"Last shutdown was not clean"* banner,
- resuming a download is refused while it is up,
- **Verify & rescan** passes the integrity check, clears the banner, and the
  library is intact afterwards.

**9. Double-launch.** With Caravan running, double-click the launcher again.
Pass: the second process refuses to start and says another Caravan is already
using the drive. Then shut down safely and confirm the marker still reads
`clean` — the second launch must not have disarmed it.

**10. Television USB browse.** Shut down safely, unplug, plug the drive into
the TV's USB port and open its media browser. Pass:

- `library/` is visible at the drive's top level,
- folders read as `Movies/` and `TV/`,
- at least one movie and one episode **play to picture and sound**,
- seeking within a playing file works.

Record the TV's make, model and firmware, and note any file that appeared but
would not play — those are transcode-profile findings for SPEC §8, not layout
failures.

**11. Round trip.** Bring the drive back to machine A, launch, and confirm the
library is unchanged and no dirty banner appears.

### Reporting

A run closes the criterion when steps 1, 3–7 and 10 pass on the named hardware.
Append the result here as a dated table: date, drive model, the three machines,
the TV model, and the per-step outcome. A failure at step 10 on one TV is a
documented client limitation (SPEC scopes DLNA and USB browsing to reference
clients, not per-TV workarounds); a failure at step 6 is a blocking bug.
