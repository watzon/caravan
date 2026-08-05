# Plan 009: Bound calendar grab and download queries to calendar items

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected result before moving to the next step. If anything in the **STOP conditions** section occurs, stop and report; do not improvise. Do not change any file outside the Scope list. When done, update this plan's status row in `plans/README.md`.
>
> **Drift check (run first)**: `git diff --stat e4426f9..HEAD -- internal/api/calendar.go internal/api/calendar_test.go internal/store/calendar.go internal/store/calendar_test.go internal/store/grabs.go internal/store/downloads.go internal/store/migrations/0002_acquisition.sql internal/store/migrations/0004_phase3.sql`
> If any in-scope file changed since this plan was written, compare the excerpts below against the live code before proceeding; on a mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: MED
- **Depends on**: none
- **Category**: perf
- **Planned at**: commit `e4426f9`, 2026-08-04

## Why this matters

The calendar already bounds movie and episode rows by the requested date range, but its status helper then loads every grab and every download in the database. A large history therefore makes every calendar request scan and decode unrelated acquisition records. Query only grabs targeting the dated movie and episode IDs, then only downloads linked to those grab IDs, while preserving the current rule that a freshly inserted grabbed row is already downloading even before a download row exists.

## Current state

- `internal/api/calendar.go:188-201` loads dated episodes and movies, then calls `downloadingCalendarItems` without passing their IDs.
- `internal/api/calendar.go:203-250` builds entries and sorts the final response by date, title, kind, season number, and episode number. Status is assigned from `HasFile` and the two downloading maps.
- `internal/api/calendar.go:255-266` gives precedence to `downloaded`, then `downloading`, then `unaired`, then `missing`.
- `internal/api/calendar.go:268-313` calls `s.st.ListGrabs(ctx, 0)` and `s.st.ListDownloads(ctx)`, indexes every download by grab ID, ignores non-`grabbed` grabs, suppresses a grabbed item only when it has downloads and every one is failed, and maps movie/episode IDs from the grab. A grab with no download row remains active.
- `internal/store/calendar.go:11-31` defines `CalendarEpisode` and `CalendarMovie`, including database-computed `HasFile` and IDs needed by the API.
- `internal/store/calendar.go:33-65` uses date bounds and monitored/file predicates for episodes; `calendar.go:68-98` uses date bounds for movies. Both methods order by date and stable title fields.
- `internal/store/grabs.go:13-14` defines the full grab projection; `grabs.go:115-143` defines `ListGrabs`, where limit zero means every grab and order is `id DESC`.
- `internal/store/grabs.go:79-112` already queries active grabs for one movie or episode. Episode targeting is stored as JSON, and `ActiveGrabForEpisode` uses `json_each`, so the schema's existing representation supports an exact episode-ID predicate.
- `internal/store/downloads.go:12-13` defines the full download projection; `downloads.go:86-107` defines `ListDownloads`, which always selects every download in `id DESC` order; `downloads.go:110-131` can select downloads for one grab but there is no multi-grab method.
- `internal/store/migrations/0002_acquisition.sql:14-27` documents `grabs.episode_ids` as a JSON array and adds indexes on `grabs.movie_id` and `grabs.series_id`. `0004_phase3.sql:13-16` adds the `grabs.status` index. There is no episode JSON index, so the filtered query must use `json_each` rather than a lossy text match.
- `internal/api/calendar_test.go:15-88` covers downloaded, missing, downloading, unaired, failed-download, and no-download grab states in one response. Its helper creates a grabbed episode with no download row and a movie with an active download plus a failed-only download.
- `internal/api/calendar_test.go:90-112` proves default and explicit date filtering; `calendar_test.go:131-169` proves ICS uses the same calendar path and status data.

The current behavior to preserve is precise: only `status == core.GrabStatusGrabbed` contributes, `state != core.DownloadFailed` is active, any non-failed download keeps the item downloading, a grab with no downloads keeps it downloading, and a failed-only set does not.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Calendar API tests | `go test -count=1 ./internal/api -run 'TestCalendar'` | exit 0; response entries and statuses match existing behavior |
| Store tests | `go test -count=1 ./internal/store -run 'Calendar|Grab|Download'` | exit 0; filtered methods select only requested IDs |
| Full Go tests | `go test -count=1 ./...` | exit 0 |
| Static checks | `go vet ./...` | exit 0 |

## Suggested executor toolkit

- Reuse `placeholders` from `internal/store/jobs.go:328-331` for dynamic `IN` lists, and return an empty slice immediately when the caller supplies no movie or episode IDs. Never generate `IN ()`.
- Keep SQL construction in `internal/store`; the API should collect IDs and preserve the existing status reduction logic.

## Scope

**In scope (the only files to modify):**

- `internal/api/calendar.go`
- `internal/api/calendar_test.go`
- `internal/store/calendar.go`
- `internal/store/grabs.go`
- `internal/store/downloads.go`
- A new `internal/store/calendar_test.go` if a focused store-level bounded-query test is needed.
- `internal/store/migrations/0002_acquisition.sql` only if an index is demonstrably required by query inspection; no schema change is expected because movie/status indexes already exist.

**Out of scope (do not touch):**

- Calendar JSON and ICS response shapes, date-range defaults, status strings, and sort order.
- Grab insertion/status transitions and download state machine semantics.
- `internal/core` types, database migrations, frontend calendar components, and API routing.
- Any source, documentation, or plan file outside this list.

## Git workflow

- Branch: `advisor/009-bounded-calendar-queries`
- Commit style: conventional commits. Use `perf: bound calendar acquisition queries`.
- Do not push or open a PR unless instructed.

## Steps

### Step 1: Add store methods for relevant grabs and linked downloads

Add narrow store methods, keeping the existing all-history methods for queue/history callers:

1. Add a method such as `ListCalendarGrabs(ctx, movieIDs, episodeIDs []int64) ([]core.Grab, error)`. It must return only `status = core.GrabStatusGrabbed` rows where `movie_id` belongs to the supplied movie IDs or `json_each(grabs.episode_ids)` contains one of the supplied episode IDs. Build both membership clauses with the existing placeholder helper and bound arguments; never concatenate IDs into SQL. Use the existing full `grabColumns` scanner. If both ID slices are empty, return an empty non-nil slice without querying. Use the existing `idx_grabs_movie_id` and `idx_grabs_status` indexes where SQLite can, and retain an `ORDER BY id DESC` tie-break for deterministic processing even though the API reduces to maps.
2. Add a method such as `ListDownloadsForGrabs(ctx, grabIDs []int64) ([]core.Download, error)`. If the slice is empty, return an empty non-nil slice. Build a parameterized `grab_id` membership clause with the existing placeholder helper, select `downloadColumns`, and order by `id DESC`. Do not include downloads with grab ID zero.
3. Do not change `ListGrabs`, `ListDownloads`, or `ListDownloadsForGrab`; other endpoints rely on their all-history or one-grab behavior.

The API needs grab rows even when there is no matching download, so the first method must not inner-join downloads. The second method runs only after the first has returned relevant grab IDs.

**Verify**: `go test -count=1 ./internal/store -run 'TestCalendar|TestListGrabs|TestListDownloads'` -> exit 0 after the Step 2 tests are added; no existing all-history behavior changes.

### Step 2: Prove the store filters and empty-input behavior

Add `internal/store/calendar_test.go` or extend the closest store acquisition tests. Use `openTemp`, create one movie and episodes, then insert:

- one grabbed movie and one grabbed season/episode grab that are in the requested IDs,
- one grabbed movie and one grabbed episode grab outside the requested IDs,
- one failed/rejected grab targeting a requested ID,
- active and failed-only downloads for selected and unrelated grab IDs,
- a download with `grab_id == 0`.

Assert `ListCalendarGrabs` returns only selected `GrabStatusGrabbed` rows, including a season-pack row when any of its JSON episode IDs is selected, and returns an empty slice for empty ID inputs. Assert `ListDownloadsForGrabs` returns only rows whose `grab_id` is in the selected set and preserves `id DESC` order.

**Verify**: `go test -count=1 ./internal/store -run Test(Calendar|ListCalendar)` -> exit 0; unrelated history rows are never returned by the new methods.

### Step 3: Pass dated IDs through the API status helper

Change `calendarEntries` to collect IDs from the already filtered `episodes` and `movies` slices, then call `downloadingCalendarItems(ctx, movieIDs, episodeIDs)`. Change that helper to:

1. call `ListCalendarGrabs` with those IDs;
2. build `byGrabID` only from `ListDownloadsForGrabs` for the returned grab IDs;
3. retain the current status reduction exactly: skip non-grabbed rows, skip a grab only when it has at least one download and all are failed, mark its movie ID and every episode ID otherwise.

Do not derive IDs from dates a second time, issue one query per item, or move SQL into `internal/api`. Keep the public `calendarResponse` and ICS output unchanged.

**Verify**: `go test -count=1 ./internal/api -run 'TestCalendarMergesEntriesAndAssignsStatuses|TestCalendarFiltersAndDefaults|TestCalendarICalRequiresKeyAndServesEvents'` -> exit 0; all existing statuses and date windows remain identical.

### Step 4: Add an API regression fixture for irrelevant history

Extend `TestCalendarMergesEntriesAndAssignsStatuses` or add a focused test that inserts many unrelated grabs/downloads, including an unrelated active download and a failed-only selected download. Request a narrow date range containing only the selected movie/episodes. Assert:

- only dated calendar rows are returned;
- the unrelated active download does not make a selected row downloading;
- a selected grab with no download remains downloading;
- a selected grab with failed-only downloads remains missing unless it has a file;
- downloaded and unaired precedence is unchanged.

The test should prove behavior, not inspect SQL text. If query-count instrumentation is available without production changes, use it; otherwise the store-level result-filter test is the authoritative bounded-query proof.

**Verify**: `go test -count=1 ./internal/api -run TestCalendar` -> exit 0.

### Step 5: Run canonical verification and inspect query shape

Run the full Go suite and vet. Review the generated SQL mentally or with the SQLite test database: no empty `IN`, no string matching against JSON, no all-history method calls remain in `downloadingCalendarItems`.

**Verify**: `go test -count=1 ./...` -> exit 0; `go vet ./...` -> exit 0.

## Test plan

- Store tests: new `internal/store/calendar_test.go` for selected movie/episode IDs, season-pack JSON matching, failed/non-grabbed exclusions, selected grab IDs, empty inputs, and deterministic order.
- API tests: existing `internal/api/calendar_test.go` plus irrelevant-history regression, preserving no-download grab and failed-only download states.
- Existing ICS and date validation tests remain unchanged and must pass.
- Verification: focused store/API tests, then `go test -count=1 ./...` and `go vet ./...` all exit 0.

## Done criteria

- [ ] Calendar status queries never call `ListGrabs(ctx, 0)` or `ListDownloads(ctx)`.
- [ ] Store queries receive only movie and episode IDs present in the date-filtered calendar rows, then only returned grab IDs are used for downloads.
- [ ] Empty movie/episode/grab ID sets produce empty slices without invalid SQL.
- [ ] Season-pack episode JSON matching uses `json_each`, not substring matching.
- [ ] A grabbed row with no download remains `downloading`; active downloads remain downloading; failed-only downloads do not; files still win over downloading.
- [ ] Calendar and ICS response shapes, date filters, sort order, and adult visibility are unchanged.
- [ ] `go test -count=1 ./...` and `go vet ./...` exit 0.
- [ ] `git status --short` shows modifications only in this plan's Scope list.

## STOP conditions

Stop and report if:

- `CalendarEpisodes` or `CalendarMovies` no longer expose stable database IDs, or calendar entries can be produced from another source not represented by those IDs.
- `grabs.episode_ids` is no longer JSON, the existing `json_each` predicate cannot be used safely, or the database lacks the documented indexes and a safe migration would be required outside scope.
- The executor cannot preserve the distinction between no download row and failed-only downloads.
- A filtered query would require loading all grabs/downloads first, adding one query per calendar row, or changing the public calendar/ICS response shape.
- Existing tests reveal a grab can target an item without `movie_id` or `episode_ids` in a way the proposed predicates would miss; stop and report the concrete fixture.
- Any status, date, adult-visibility, or ICS behavior changes without an explicit requirement.

## Maintenance notes

- Keep `ListGrabs` and `ListDownloads` for history and queue consumers; this optimization is intentionally calendar-specific.
- If calendar gains another item kind, add its IDs to the first filtered query and preserve the same grab-before-download reduction.
- A future normalized grab-episode join table could improve JSON query indexing, but it is not needed for this bounded query plan and is explicitly deferred.
- Reviewers should confirm the API obtains IDs from the already date-filtered rows and that a no-download grab is not accidentally lost by an inner join.
