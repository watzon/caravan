# Plan 007: Reuse TMDB clients by API-key cache key

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected result before moving to the next step. If anything in the **STOP conditions** section occurs, stop and report; do not improvise. Do not change any file outside the Scope list. When done, update this plan's status row in `plans/README.md`.
>
> **Drift check (run first)**: `git diff --stat e4426f9..HEAD -- cmd/caravan/manager.go cmd/caravan/acquisition_test.go internal/tmdb/browse.go internal/tmdb/browse_test.go`
> If any in-scope file changed since this plan was written, compare the excerpts below against the live code before proceeding; on a mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: S
- **Risk**: MED
- **Depends on**: none
- **Category**: perf
- **Planned at**: commit `e4426f9`, 2026-08-04

## Why this matters

`internal/tmdb.Client` owns the genre cache used by the Explore filter rail, but `libraryAdapter.metadata` constructs a new client every time an API operation builds a `library.Manager`. The cache therefore survives only within one call and repeated GETs re-fetch the same fixed genre vocabulary. Reuse the client while its stored API key is unchanged, rebuild it when the key changes or is cleared, and keep candidate-key validation throwaway so an uncommitted key cannot evict a working client.

## Current state

- `cmd/caravan/manager.go:37-59` has `libraryAdapter` fields and a mutex/cache only for the adult Stashbox client. There is no TMDB cache field.
- `cmd/caravan/manager.go:82-92` calls `a.metadata(ctx)` every time it builds a library manager.
- `cmd/caravan/manager.go:218-238` reads `SettingTMDBAPIKey`, returns nil for empty, and otherwise calls `tmdb.New(key, a.hc)`. The function comment says late binding is achieved by building a client per call.
- `cmd/caravan/manager.go:240-249` deliberately constructs `tmdb.New(apiKey, a.hc).Test(ctx)` for `ValidateMetadataKey`, because the candidate may not be stored yet. This path must remain uncached.
- `internal/tmdb/client.go:95-114` stores the API key and a `genreCache` on each client. `internal/tmdb/client.go:120-133` creates a fresh empty cache in `New`.
- `internal/tmdb/browse.go:261-315` defines `genreCache` and `Client.Genres`. A successful list is cached per media type, a failed fetch is not cached, and unsupported media types return `ErrUnsupportedMediaType` without a request.
- `internal/tmdb/browse_test.go:325-363` proves each media type is fetched once per client; `browse_test.go:376-399` proves failures are not cached.
- `cmd/caravan/acquisition_test.go:23-58` provides `testAdapter` and proves late metadata follows a key added after startup. `cmd/caravan/smoke_test.go:302-343` supplies `startFakeTMDB` and `redirectTMDB` helpers.
- `cmd/caravan/adult_test.go:278-370` is the closest composition-root cache pattern: call the provider through the adapter twice, assert one capability probe, rotate the key/endpoint, and assert a new client. `adult_test.go:372-411` verifies concurrent callers receive one shared client. `adult_test.go:413-477` proves candidate validation does not evict the working cache.

The intended cache key is exactly the stored TMDB API key. No endpoint or image-base setting is read from storage, and `tmdb.New` receives only `(key, a.hc)`, so no second key component is justified.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Adapter tests | `go test -count=1 ./cmd/caravan -run 'Test(LateMetadata|TMDB|Metadata)'` | exit 0; reuse, rotation, clearing, concurrency, and candidate validation tests pass |
| TMDB package tests | `go test -count=1 ./internal/tmdb` | exit 0; existing genre cache tests remain green |
| Race check | `go test -race -count=1 ./cmd/caravan -run 'TestTMDB|TestMetadata'` | exit 0 with no race report |
| Full Go tests | `go test -count=1 ./...` | exit 0 |

## Suggested executor toolkit

- Mirror `cachedStashbox` and `stashboxClient` in `cmd/caravan/manager.go`; do not add a generic cache abstraction for one additional client.
- Use the existing fake TMDB redirect helpers. Tests must inspect request counts and query `api_key` values without logging real-looking secrets.

## Scope

**In scope (the only files to modify):**

- `cmd/caravan/manager.go`
- `cmd/caravan/acquisition_test.go`
- `internal/tmdb/browse_test.go` only if an additional client-lifetime assertion belongs at the package seam; existing tests should be reused rather than duplicated.

**Out of scope (do not touch):**

- `internal/tmdb/client.go` and `internal/tmdb/browse.go`; their cache already has the required lifetime semantics.
- `internal/api` handlers and store settings; late binding and credential state behavior are unchanged.
- `cmd/caravan/smoke_test.go`; use its existing helpers without editing them.
- Any frontend, documentation, or plan file outside this plan.

## Git workflow

- Branch: `advisor/007-tmdb-client-cache`
- Commit style: conventional commits, matching recent history. Use `fix: reuse TMDB clients for genre caching`.
- Do not push or open a PR unless instructed.

## Steps

### Step 1: Add a key-aware TMDB cache to the adapter

Add a small cache beside `adultMu` in `libraryAdapter`, with a mutex and a cached key/client pair. The cache helper should follow these rules:

1. `metadata(ctx)` still reads `SettingTMDBAPIKey` on every call, so a setting edit takes effect without restart.
2. A read error logs and returns a genuine untyped nil, as now. An empty key clears the cached TMDB pair and returns nil.
3. A non-empty key returns the existing `*tmdb.Client` when the cached key matches. A changed key replaces the pair with `tmdb.New(key, a.hc)`.
4. Lock only around cache lookup/replacement. Do not hold the mutex while reading settings or making HTTP requests.
5. `ValidateMetadataKey` remains exactly a direct `tmdb.New(candidate, a.hc).Test(ctx)` call. It must not call the cache helper and must not modify the cached pair.

Keep `lateMetadata` behavior unchanged: it asks `adapter.Metadata()` per provider operation, so a key added after watcher startup is visible and a cleared key degrades to `ErrNoMetadataProvider`.

**Verify**: `go test -count=1 ./cmd/caravan -run TestLateMetadataFollowsTheSettingsTable` -> exit 0; the existing late-binding guarantee still holds.

### Step 2: Prove reuse, invalidation, and concurrency at the adapter seam

Add focused tests to `cmd/caravan/acquisition_test.go`, following the Stashbox tests' fixture style:

- **Reuse and genre cache**: point TMDB at an `httptest.Server` that answers `/genre/movie/list`, store key `working`, obtain `adapter.Metadata()` twice, call `Genres(movie)` through each provider, and assert one request. Assert the returned interface pointers are the same `*tmdb.Client` where type assertion is safe.
- **Key rotation**: after warming key `working`, store `rotated`, obtain metadata, call `Genres(movie)`, and assert a second request whose query key is `rotated`; the old key must not answer the new request.
- **Clear and re-add**: clear the setting, assert `Metadata()` is nil, restore the old key, obtain metadata and call `Genres(movie)`, and assert a fresh request. This proves clearing invalidates rather than leaving a same-key client alive across a credential removal.
- **Concurrent callers**: start several goroutines calling `adapter.metadata(ctx)` with one stored key, assert every non-nil provider points to the same client, and run under `-race`. The test need not issue network requests; its contract is shared identity and race safety.
- **Candidate validation**: warm the working client and its genre cache, call `ValidateMetadataKey` with a different candidate, then call `Genres(movie)` again. The count must increase only for the candidate's `/configuration` test; the working client must not be rebuilt and the genre endpoint must not be fetched again. If the fake server cannot distinguish `/configuration` from genres, add request-path counters in the test fixture.

Use non-sensitive sentinel labels such as `working-key` and `candidate-key`; do not paste actual credentials in the plan or implementation logs.

**Verify**: `go test -race -count=1 ./cmd/caravan -run 'Test(LateMetadata|TMDB|Metadata)'` -> exit 0 with all assertions and no race report.

### Step 3: Confirm the package cache contract remains unchanged

Run the existing TMDB browse tests. Do not alter `genreCache` locking, failure behavior, or media-type validation merely to support the adapter cache. If a new test is needed, put it in `internal/tmdb/browse_test.go` only to prove a client passed through unchanged retains both media-type entries.

**Verify**: `go test -count=1 ./internal/tmdb` -> exit 0; `TestGenresAreFetchedOncePerMediaType`, `TestGenresDoNotCacheAFailure`, and `TestGenresRejectsAnUnknownMediaType` remain green.

### Step 4: Run the repository smoke checks

Run the canonical Go checks after the focused tests. This change is backend-only, so no frontend commands are required by this plan.

**Verify**: `go test -count=1 ./...` -> exit 0; `go vet ./...` -> exit 0.

## Test plan

- Primary new tests: `cmd/caravan/acquisition_test.go`, using `testAdapter`, `redirectTMDB`, and an `httptest.Server` with path/key counters.
- Structural pattern: `cmd/caravan/adult_test.go:282-411` for reuse, key change, and concurrent identity; `adult_test.go:420-477` for candidate validation not evicting the working client.
- Required cases: no key, key set after startup, same-key repeated acquisition, genre cache across two adapter calls, changed key, cleared key, restored key, concurrent calls, candidate validation, failed genre fetch not cached (existing package test).
- Verification: focused race test, package tests, then `go test -count=1 ./...` and `go vet ./...` all exit 0.

## Done criteria

- [ ] `libraryAdapter` returns the same TMDB client for repeated acquisitions with the same non-empty stored key.
- [ ] The client is replaced when the key changes and invalidated when the setting becomes empty.
- [ ] `ValidateMetadataKey` always uses a throwaway client and cannot evict or replace the working cached client.
- [ ] Existing late-binding behavior still observes keys added or removed after startup.
- [ ] Genre requests are one per media type per cached client, including across separate adapter/current calls.
- [ ] Concurrent metadata callers share one client and pass `go test -race`.
- [ ] `go test -count=1 ./...` and `go vet ./...` exit 0.
- [ ] `git status --short` shows changes only in this plan's Scope list.

## STOP conditions

Stop and report if:

- `tmdb.Client` no longer owns `genreCache` or `tmdb.New` accepts additional key-defining arguments not present in the excerpts.
- A cache implementation would need to read a candidate key from storage or cache a key before validation; do not weaken the throwaway validation boundary.
- Empty-key handling cannot clear the cached client without changing an out-of-scope API or store contract.
- Concurrent calls can observe two clients for one unchanged key, or `-race` reports a data race.
- The fake TMDB cannot prove which key each request used without logging or exposing the key in an assertion message.
- Any change appears necessary in `internal/tmdb/client.go` or `browse.go` to make the existing genre cache work; stop and report drift instead of broadening scope.

## Maintenance notes

- The cache is intentionally process-local and unbounded at one entry: replacing a key drops the old client and its genre cache for garbage collection.
- Reviewers should check the cache key is the exact stored key, the empty-key path invalidates, and validation bypasses the cache.
- If TMDB gains another runtime setting that changes client behavior, add it to the cache key or explicitly document why it does not affect the client. Do not silently reuse across such a change.
- The existing TMDB client cache is not a persistent metadata cache; library-layer SQLite caching remains responsible for provider responses.
