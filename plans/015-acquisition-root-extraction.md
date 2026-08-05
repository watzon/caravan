# Plan 015: Extract the acquisition composition root incrementally

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected result before moving to the next step. If anything in STOP conditions occurs, stop and report; do not improvise. When done, update this plan's status row in `plans/README.md`.
>
> **Drift check (run first)**: `git diff --stat e4426f9..HEAD -- cmd/caravan/acquisition.go cmd/caravan/manager.go cmd/caravan/serve.go cmd/caravan/acquisition_test.go cmd/caravan/smoke_test.go cmd/caravan/smoke_usenet_test.go`

## Status

- **Priority**: P3
- **Effort**: L
- **Risk**: HIGH
- **Depends on**: `plans/003-concurrent-client-polling.md`, `plans/008-atomic-jellyfin-settings.md`
- **Category**: tech-debt
- **Planned at**: commit `e4426f9`, 2026-08-04

## Why this matters

`cmd/caravan` is the composition root and currently owns storage-backed adapters, lazy embedded engines, external-client routing, health/admission state, import watching, metadata late binding, and the entire process lifecycle. That central ownership is correct, but the large files make lifecycle ordering and shutdown changes hard to review. Extract by ownership/lifecycle boundaries only, with characterization tests before each move; preserve behavior and accept that this is low-priority, high-risk work.

## Current state

- `cmd/caravan/acquisition.go` is approximately 995 lines. It defines the process-wide client registration (`registerDownloadClients`, lines 44-59), indexer construction (`newIndexerFactory`, lines 61-69), the store-backed `downloadPersistence` seam (lines 71-94), and the `engineProvider` state machine (lines 96-168).
- `acquisition.go:171-197` owns the external-client type switch (`newDownloadClientEngine`) and credential-inclusive fingerprint; `acquisition.go:218-353` exposes `Engine`, `EngineFor`, and `routesFor`; `acquisition.go:355-498` builds client routes, health transitions, caches, and closes edited/disabled clients.
- `acquisition.go:508-590` resolves library/global route picks and lazily constructs the embedded torrent engine; `acquisition.go:592-697` constructs embedded Usenet, synchronizes servers, and closes engines; `acquisition.go:699-889` applies settings/caps and seeds external waiting work; `acquisition.go:891-930` awaits an engine and runs the import watcher.
- `acquisition.go:932-994` defines `lateMetadata` and compile-time interface assertions. The file mixes pure translation helpers, mutable caches, construction, routing, health/event side effects, and lifecycle shutdown.
- `cmd/caravan/manager.go:22-80` defines `libraryAdapter` and its constructor; `manager.go:82-186` builds per-request and watcher managers, including adult-provider cache/late binding; `manager.go:188-263` owns settings/provider resolution and credential validation; `manager.go:266-343` delegates scan/add/sync operations. This is an existing seam, not a reason to duplicate managers.
- `cmd/caravan/serve.go:36-121` loads config, claims the integrity marker, opens/checkpoints/closes the store, and establishes shutdown ordering. `serve.go:158-244` constructs Jellyfin/Stash handoffs, `libraryAdapter`, `engineProvider`, indexers, automation, conversion, DLNA, relocation, and the runner. `serve.go:266-315` starts the two long-lived goroutines, builds `api.NewServer`, starts DLNA, waits for signals, and drains watchers.
- Existing constructor patterns are explicit and dependency-injected: `jellyfin.NewService(st, nil, logger)` (`serve.go:168`), `stash.NewService(st, nil, logger)` (`serve.go:175`), `convert.New(st, mgr.StorageRoot, convert.Detect(), logger)` (`serve.go:202`), and `relocate.New(st, engines.Engine, logger)` (`serve.go:223`). The longer `automation.NewRunner` call at `serve.go:229-264` and `api.NewServer` call at `serve.go:279-299` follow the same explicit-wiring pattern. Match it; do not introduce a container.
- Characterization coverage already exists: `cmd/caravan/acquisition_test.go:33-114` proves late metadata and lazy engine/close behavior; lines 147-201 prove watcher imports notify Jellyfin; lines 203-221 prove all external clients register; `cmd/caravan/smoke_test.go:64-232` proves real acquisition, import, restart, and download removal behavior.
### Exact excerpts

```go
// cmd/caravan/acquisition.go:159-168
func newEngineProvider(adapter *libraryAdapter, paused bool, log *slog.Logger) *engineProvider {
    return &engineProvider{
        adapter: adapter,
        paused: paused,
        log: log,
        newClientEngine: newDownloadClientEngine,
        external: map[int64]*clientEngine{},
        health: download.NewHealth(download.DefaultUnhealthyAfter),
        admission: download.NewAdmission(download.Caps{}),
    }
}
```

```go
// cmd/caravan/serve.go:177-185
mgr := newLibraryAdapter(st, cfg.StorageRoot, logger, handoff, stashHandoff)
engines := newEngineProvider(mgr, cfg.Portable, logger)
defer func() {
    if err := engines.Close(); err != nil {
        logger.Error("closing download engine", "error", err)
    }
}()
```

```go
// cmd/caravan/serve.go:266-275
var watcher sync.WaitGroup
watcher.Add(2)
go func() {
    defer watcher.Done()
    runImportWatcher(ctx, engines, mgr, logger)
}()
```

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Composition tests | `go test -count=1 ./cmd/caravan` | exit 0; adapter/provider/lifecycle tests pass |
| Full Go tests | `go test -count=1 ./...` | exit 0 |
| Vet | `go vet ./...` | exit 0 |
| Short composition smoke | `go test -short -count=1 ./cmd/caravan` | exit 0; long real-transfer smoke is skipped |
| Race characterization | `go test -race -count=1 ./cmd/caravan` | exit 0; no races in provider/serve lifecycle tests |

## Scope

**In scope (the only files to modify):**

- `cmd/caravan/acquisition.go` and new sibling files under `cmd/caravan/` split by ownership
- `cmd/caravan/manager.go` only when extracting a clearly owned adapter/provider seam
- `cmd/caravan/serve.go` only to call extracted constructors/lifecycle helpers
- `cmd/caravan/acquisition_test.go`, `cmd/caravan/smoke_test.go`, and new characterization tests under `cmd/caravan/`

**Out of scope:**

- Any `internal/*` subsystem API redesign, routing semantics, persistence schema, or engine behavior change.
- New download-client backends, new metadata providers, or feature work.
- Changing shutdown order, lazy construction, settings late binding, or event messages except where characterization proves an existing bug.
- Mechanical splitting solely by line count; each new file must own a lifecycle/state boundary.

## Git workflow

- Branch: `advisor/015-acquisition-root-extraction`
- Commit style: conventional commits; use `refactor: extract acquisition composition boundaries`.
- Do not push or open a PR.

## Steps

### Step 1: Freeze characterization and lifecycle evidence

Run the existing `cmd/caravan` tests and read the complete startup/defer ordering. Add only behavior tests needed to pin: first-run engine nil until storage root, one embedded engine after root, external engine rebuild/close on config fingerprint changes, router default resolution, client health transition events, watcher metadata late binding, and close-before-store ordering. Do not extract code before these assertions exist.

**Verify**: `go test -short -count=1 ./cmd/caravan` → exit 0; each named invariant has a deterministic test or an existing test citation.

### Step 2: Extract pure acquisition helpers first

Move low-risk, dependency-light functions into an acquisition support file/package in the same `main` package: `clientMethod`, `clientIDPrefix`, `routePick`, `capsFrom`, `engineSettingInt`, `engineSettingInt64`, `engineSettingFloat`, `clientFingerprint`, and `newDownloadClientEngine`. Preserve names/visibility initially to avoid call-site churn, and keep the external-client type switch at the composition boundary. Verify no credentials enter logs or error strings.

**Verify**: `go test -count=1 ./cmd/caravan && go vet ./...` → exit 0; no behavior test changes beyond file location.

### Step 3: Extract the external-client lifecycle owner

Create a focused owner for `external` client engines, fingerprints, route construction, health observation/events, sync, wake registration, and close (`clientEngine`, `routedEngine`, `syncClientEngines`, `clientRoute`, `observeClient`, `recordClientEvent`, `closeClientEngine`, `registerClientWake`, `seedWaiting`). Inject the existing `store.Store`, health tracker, admission coordinator, engine factory, and logger rather than reaching for globals. Keep `engineProvider` as the caller-facing seam until all call sites are migrated.

**Verify**: `go test -race -count=1 ./cmd/caravan -run 'Client|Routing|EngineProvider'` → exit 0; external routes, health events, and close behavior match characterization expectations.

### Step 4: Extract embedded-engine lifecycle ownership

Move lazy embedded torrent/Usenet construction, settings synchronization, caps, and close (`embedded`, `embeddedUsenet`, `syncUsenetServers`, `engineOptions`, `applyCaps`, `seedWaiting` portions that are provider-owned) behind a provider component with the same `Engine`/`EngineFor` seam. Preserve the root capture rule, portable paused behavior, one-time seed restoration, and close-before-store ordering. Do not make construction eager: first run must still serve setup with no storage root.

**Verify**: `go test -count=1 ./cmd/caravan -run 'EngineProvider|EngineOptions|Lazy|Close'` → exit 0; no engine is built before a root exists and exactly one is built after it is configured.

### Step 5: Extract watcher and metadata binding without changing cadence

Move `runImportWatcher`, `await`, `lateMetadata`, and their helper assertions into a watcher-owned file/owner that receives the already-built provider, adapter, and logger. Keep the watcher’s fixed startup manager root, per-call metadata resolution, five-second engine wait cadence, event timeout, and two-goroutine shutdown contract. Do not make the HTTP request path depend on the watcher owner.

**Verify**: `go test -count=1 ./cmd/caravan -run 'ImportWatcher|LateMetadata'` → exit 0; a TMDB key set after startup reaches the next import and automatic import still notifies Jellyfin/Stash seams.

### Step 6: Extract `runServe` assembly last

Introduce a small composition struct or constructor that assembles the existing handoffs, adapter, providers, converter, DLNA, relocator, runner, and API options in the current order. It must expose explicit `Start`/`Close` ownership only where an existing subsystem already has that lifecycle. Keep integrity marker/store checkpoint defers in `runServe` until an ordering characterization test proves they can move safely.

**Verify**: `go test -count=1 ./cmd/caravan && go test -short -count=1 ./cmd/caravan` → exit 0; startup, API wiring, and clean shutdown remain identical.

### Step 7: Run full and race gates, then review diff

Run full tests/vet and the targeted race test, inspect all moved symbols and imports, and compare the smoke behavior before/after. Keep this refactor in small commits so a lifecycle regression can be bisected.

**Verify**: `go test -count=1 ./... && go vet ./... && go test -race -count=1 ./cmd/caravan && git diff --check` → all exit 0; only Scope files changed.

## Test plan

- Characterization tests in `cmd/caravan/acquisition_test.go` are the primary pattern; preserve real store/temp-dir setup and fake engine seams.
- Extend lifecycle coverage for provider close ordering and an external-client config edit that closes the old engine exactly once.
- Keep `cmd/caravan/smoke_test.go` as the end-to-end proof and run it only without `-short` when an operator explicitly wants the transfer test; the refactor must not alter its fixture or network assumptions.
- Run `go test -race -count=1 ./cmd/caravan` before full tests to expose shared-cache regressions early.

## Done criteria

- [ ] Composition responsibilities are split by ownership/lifecycle boundary, not arbitrary file size.
- [ ] Existing provider, adapter, watcher, startup, and shutdown behavior is characterized and preserved.
- [ ] No `internal/*` API or product behavior changes were needed.
- [ ] `go test -count=1 ./...`, `go vet ./...`, and `go test -race -count=1 ./cmd/caravan` pass.
- [ ] The long smoke still passes when run explicitly without `-short`.
- [ ] Changes are small, bisectable commits on `advisor/015-acquisition-root-extraction`.

## STOP conditions

- A move requires changing shutdown/defer order without a failing characterization test that explains why.
- Any extraction needs a new global registry, service locator, or internal package cycle.
- Lazy engine creation, late settings binding, route defaults, health events, or portable pause semantics change.
- Race failures appear and cannot be fixed without redesigning subsystem synchronization.
- A proposed split changes an `internal/*` API or touches files outside Scope.

## Maintenance notes

- This is intentionally low priority and high risk; defer it if feature or correctness work is active in `cmd/caravan`.
- Keep `runServe` visibly responsible for process-wide lifecycle and `engineProvider` visibly responsible for engine ownership; reviewers should reject “utility” files that blur either.
- Any new long-lived goroutine needs an owner, cancellation path, wait-group evidence, and a characterization test before landing.
- Revisit extraction boundaries after future external-client or portable-release work; do not let release packaging logic enter this composition root.
