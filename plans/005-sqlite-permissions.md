# Plan 005: Restrict SQLite state-file permissions

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected result before moving to the next step. If anything in the **STOP conditions** section occurs, stop and report; do not improvise. When done, update this plan's status row in `plans/README.md`.
>
> **Drift check (run first)**: `git diff --stat e4426f9..HEAD -- internal/store/store.go internal/store/store_test.go`
> If any in-scope file changed since this plan was written, compare the **Current state** excerpts against the live code before proceeding. A mismatch is a STOP condition.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: HIGH
- **Depends on**: none
- **Category**: security
- **Planned at**: commit `e4426f9`, 2026-08-04

## Why this matters

`store.Open` lets SQLite create a new database using the process umask and never repairs permissions on an existing database or its WAL and SHM sidecars. A permissive umask can therefore make `caravan.db` readable by other users, while WAL and SHM files can carry the same application state and credentials. Open the database with a 0600 creation mode, repair existing database and sidecar modes on every open, and test both new installs under a permissive umask and existing installs with loose modes without losing data. Permission assertions must be platform-aware because Windows does not expose POSIX mode bits with the same meaning.

## Current state

Relevant files and roles:

- `internal/store/store.go` - opens the SQLite handle, enables WAL/foreign keys/busy timeout, and runs migrations.
- `internal/store/store_test.go` - uses `openTemp` for temporary database lifecycle, checks migrations/WAL/foreign keys, and verifies reopen persistence.
- `internal/store/migrations/0001_init.sql` - creates the persistent schema; no schema change is needed for filesystem modes.
- `cmd/caravan/serve.go` - passes the configured `caravan.db` path to `store.Open` and checkpoints the WAL during shutdown; it should not own permission policy.
- `.gitignore` - confirms `caravan.db`, `caravan.db-wal`, and `caravan.db-shm` are runtime state.

The current open path does not set a file mode or inspect sidecars:

```go
// internal/store/store.go:30-57
// Open opens (creating if needed) the sqlite database at path and runs every
// pending migration. WAL journaling is enabled so readers never block the
// writer, and foreign keys are enforced so the cascade rules in the schema
// actually fire.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	// sqlite takes a single writer. Serializing at the pool removes SQLITE_BUSY
	// as a class of failure at the cost of write concurrency we do not have anyway.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: connect %s: %w", path, err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
```

The DSN enables WAL, but does not set file permissions:

```go
// internal/store/store.go:60-72
func dsn(path string) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(NORMAL)")
	return "file:" + path + "?" + q.Encode()
}
```

The store deliberately keeps WAL checkpoint policy in the caller:

```go
// internal/store/store.go:75-94
// Close closes the database. Portable mode checkpoints the WAL before this
// (SPEC §2.3); that is the caller's job because it is a shutdown-policy
// decision, not a connection one.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Checkpoint() error {
	if _, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("store: wal checkpoint: %w", err)
	}
	return nil
}
```

The existing test helper establishes the store lifecycle pattern to extend:

```go
// internal/store/store_test.go:14-23
func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "caravan.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() { st.Close() })
	return st, path
}
```

`internal/store/store_test.go:73-124` proves that closing and reopening preserves the storage-root setting, schema version, and single seeded quality profile. Extend that pattern with permission assertions; do not replace its persistence checks.

Current WAL coverage only checks the SQLite pragma, not sidecar modes:

```go
// internal/store/store_test.go:53-71
func TestOpenEnablesWALAndForeignKeys(t *testing.T) {
	st, _ := openTemp(t)
	var mode string
	if err := st.DB().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want %q", mode, "wal")
	}
	var fk int
	if err := st.DB().QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}
```

The runtime files are explicitly grouped in the repository ignore policy:

```text
# .gitignore:1-5
# sqlite database and its WAL/SHM sidecars
caravan.db*
```

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Drift | `git diff --stat e4426f9..HEAD -- internal/store/store.go internal/store/store_test.go` | Empty output, or reviewed changes matching the excerpts above |
| Store permission tests | `go test -count=1 ./internal/store -run 'Test(Open|Existing|SQLite|Database|WAL).*Permission|TestReopenIsIdempotent'` | Exit 0; new and existing installs retain data and have 0600 database artifacts on POSIX platforms |
| Store package regression | `go test -count=1 ./internal/store` | Exit 0; migrations, WAL, CRUD, checkpoint, and permission tests pass |
| Static check | `go vet ./internal/store` | Exit 0 |
| Full Go regression | `go test -count=1 ./...` | Exit 0 |

## Scope

**In scope (the only files to modify):**

- `internal/store/store.go` - add 0600 creation and repair of the main database plus `-wal` and `-shm` sidecars, with clear error handling.
- `internal/store/store_test.go` - add permissive-umask, existing-install, sidecar, and platform-aware permission assertions using the existing temporary-store patterns.

**Out of scope (do not touch):**

- SQLite schema/migrations, DSN pragma values, checkpoint policy, config path selection, portable marker permissions, library media permissions, Docker user setup, or API behavior.
- New dependencies for filesystem utilities; use the Go standard library already used by `store.go` and tests.
- Source files outside the Scope list, plans index, README, or broad security hardening.

## Git workflow

- Branch: `advisor/005-sqlite-permissions`
- Commit style: conventional commits, matching recent history; use `fix: restrict SQLite state file permissions`.
- Do not push or open a PR unless the operator instructs it.

## Steps

### Step 1: Define the SQLite artifact permission policy

In `internal/store/store.go`, establish a single private helper for the main path and the two SQLite sidecars. The policy is:

1. Before `sql.Open`, ensure a newly created main database is opened with `os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)` and close that bootstrap descriptor. This makes creation safe even when the process umask is zero.
2. Apply `os.Chmod(path, 0o600)` to existing databases as well as new ones. Do not replace or truncate an existing file, and preserve existing database contents.
3. After SQLite has connected and migrations have run, apply the same mode to `path + "-wal"` and `path + "-shm"` when those files exist. Ignore only `os.IsNotExist` for a sidecar that SQLite has not created; return other permission or filesystem errors and close the SQL handle.
4. Keep the helper in the store boundary and call it on every `Open`, not only first install. Do not put this in `serve.go`, because tests and every other caller use `store.Open` directly.

The exact ordering must ensure that an existing loose sidecar is repaired during Open, while a new sidecar created by WAL is repaired after the first connection/migration write. Do not call `Checkpoint` from `Open`; shutdown owns that policy.

**Verify**: `go test -count=1 ./internal/store -run 'Test.*Permission'` -> exit 0 on POSIX platforms; a permissive umask cannot create a group/world-readable main database.

### Step 2: Test new installs under a permissive umask

In `internal/store/store_test.go`, add a test that is not parallel because process umask is global. Save the current umask, set `syscall.Umask(0)`, defer restoration, call `Open` on a temporary path, write a setting, and inspect `os.Stat(path).Mode().Perm()`. On POSIX platforms the result must be exactly `0o600`, not merely no more permissive than 0600. Close the store and, if WAL/SHM exist, assert those sidecars are also exactly 0600.

Use the existing `openTemp` lifecycle pattern but manage the umask and path explicitly so cleanup cannot race another test. Do not leave the process umask changed after the test, even if an assertion fails.

**Verify**: `go test -count=1 ./internal/store -run TestNewSQLiteFilesArePrivate` -> exit 0; the test passes with umask 000 and restores the caller's umask.

### Step 3: Test existing installs and sidecars

Add a test that creates a valid store, writes a setting, closes it without deleting the database, then makes the main database and any existing `-wal`/`-shm` files permissive with `os.Chmod(artifactPath, 0o644)`. Reopen through `Open`, assert the setting is still present, and assert every existing artifact is 0600. Force a WAL write before closing if needed so the test exercises real sidecars; do not rely on arbitrary invalid SQLite sidecar bytes.

Add a platform guard around POSIX mode assertions. On Windows, run the open/reopen/data-preservation part but skip exact permission-bit assertions because `FileMode.Perm` does not represent Windows ACLs. On any non-Windows platform where `os.Chmod` or mode inspection is unavailable or returns an unexpected error, stop and report rather than treating the security contract as passing.

**Verify**: `go test -count=1 ./internal/store -run TestExistingSQLiteFilesAreHardened` -> exit 0; existing data survives and all present SQLite artifacts are private on POSIX platforms, while Windows performs the non-destructive lifecycle check.

### Step 4: Review error cleanup and run regressions

Review `Open` for every failure path: bootstrap open failure, `sql.Open`, Ping, hardening, and migration must close descriptors/SQL handles before returning. Confirm sidecar absence is the only ignored artifact error, and a chmod failure is visible to the caller rather than silently logged. Run the full store package, vet, and canonical Go suite.

**Verify**: `go vet ./internal/store && go test -count=1 ./internal/store && go test -count=1 ./...` -> all commands exit 0.

## Test plan

- `internal/store/store_test.go`: a non-parallel permissive-umask test for newly created database and sidecars; an existing-install test for repairing 0644 main/WAL/SHM while preserving rows; retain `TestOpenEnablesWALAndForeignKeys`, `TestReopenIsIdempotent`, and `TestCheckpoint`.
- POSIX behavior: exact `Mode().Perm() == 0o600` for every present SQLite artifact.
- Windows behavior: still exercise Open, migration, write, close, reopen, and data preservation; skip POSIX mode-bit assertions with an explicit reason.

Final verification: `go test -count=1 ./internal/store` and `go test -count=1 ./...` -> all pass.

## Done criteria

- [ ] A new `caravan.db` is created with mode 0600 even under umask 000 on POSIX platforms.
- [ ] Existing `caravan.db`, `caravan.db-wal`, and `caravan.db-shm` files are repaired to 0600 when present.
- [ ] Missing sidecars are not treated as errors, and future sidecars created by WAL are hardened on Open.
- [ ] Existing database rows survive hardening and reopen.
- [ ] Chmod/open/migration failures close resources and return an actionable error; no failure is silently ignored except absent sidecars.
- [ ] Windows tests do not claim POSIX mode semantics, while non-destructive store lifecycle behavior still passes.
- [ ] `go vet ./internal/store` and `go test -count=1 ./...` exit 0.
- [ ] `plans/README.md` marks plan 005 `DONE` after every gate passes.

## STOP conditions

Stop and report instead of improvising if:

- The `store.Open`, DSN, or existing store test excerpts no longer match after the drift check.
- SQLite creates or uses additional state files beyond `caravan.db`, `-wal`, and `-shm`; identify them before claiming the artifact set is complete.
- The database path is a symlink, special file, network filesystem, or ACL-backed path whose safe hardening semantics are not established. Do not follow a symlink and chmod an unintended target.
- `os.Chmod` cannot enforce 0600 on a supported POSIX target, or a permissive-umask test remains group/world-readable after the implementation; stop rather than weakening the assertion.
- Windows or another target requires ACL APIs or a platform-specific implementation not available in the listed files; stop and report the missing platform decision instead of pretending mode bits provide security.
- A sidecar chmod failure is being ignored to keep startup working, or making it fatal would require touching an out-of-scope startup/error policy file.
- Existing installs cannot be repaired without rewriting, truncating, checkpointing, or otherwise risking database contents.
- The implementation requires changing migrations, config, Docker, portable marker handling, or another plan file.

## Maintenance notes

- Keep artifact hardening at `store.Open`; all callers then receive the same policy, including tests, API servers, portable mode, and command startup.
- New SQLite sidecar files or a change in journaling mode must update the artifact list and tests before release.
- Permission modes do not replace filesystem ownership, ACLs, encrypted volumes, or secret rotation. This plan addresses accidental local disclosure by loose POSIX modes only.
- Preserve the existing checkpoint boundary. Hardening on Open is not a request to change WAL durability or shutdown behavior.
