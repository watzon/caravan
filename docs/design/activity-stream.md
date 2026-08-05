# Read-only activity stream spike

Status: reviewed spike design. This document is not an endpoint specification for immediate implementation.

## Decision

**Outcome B: prototype a narrow read-only invalidation/activity stream later, while keeping every existing REST snapshot and poller as the fallback.** The disposable harness shows that a bounded publisher can deliver local hints without blocking a durable writer, and that a bounded event replay can recover a reconnect. It does not justify replacing polling or introducing a second state model.

Review record: 2026-08-05 UTC; OpenAI Codex execution agent acting as implementation owner and maintainer-role reviewer. No human sign-off is represented by this record. Evidence: `tmp/activity-stream-spike/measurements.tsv` and `tmp/activity-stream-spike/summary.txt`.

Measured values from the local disposable harness:

- Authentication: unauthenticated `401`, member `403`, admin session `200`, admin `X-Api-Key` `200`, query-string key `400`.
- Publish-to-subscriber latency: p50 `209 ns`, p95 `667 ns` for 100 in-process publishes.
- One-event reconnect replay: `73 us`; framed event size: `41 bytes`.
- Retained replay window: `32` events. An old cursor emitted `reset` and caused exactly one bounded snapshot GET.
- Slow subscriber: channel capacity `8`; `24` coalescible hints dropped without blocking the publisher.
- Local burst before drop/coalesce: `9,463,719 events/sec` for 10,000 hints.

These are protocol and local-process measurements, not production capacity claims. A future implementation must repeat them with the actual HTTP server, browser count, event volume, and deployment limits before launch.

## Current producer and consumer inventory

The inventory was collected from the repository search required by the plan. Test-only `InsertEvent`/`ListEvents` calls validate the same contracts but are not production producers.

### Durable event producers

| Category | Writers | Durable fields and meaning |
|---|---|---|
| `system` | `internal/api/system.go` shutdown, integrity failure, and verification; `internal/api/auth.go` failed login | Process/security activity. `level`, message/detail, timestamp; failed-login detail must remain free of credentials. |
| `download` | `cmd/caravan/acquisition.go` client health transitions; `internal/api/downloads.go` removal | Queue/client activity. Optional movie/series IDs where available. |
| `grab` | `internal/api/releases.go`; `internal/automation/handlers.go` | Release accepted, refused, or failed; optional movie/series IDs. |
| `import` | `internal/library/importdownload.go`, `internal/library/library.go`, `internal/library/watch.go` | Import progress, manual match, watcher errors, and handoff warnings; optional media IDs. |
| `library` | `internal/library/remove.go` | Refused library path deletion; detail is bounded and path policy remains the source of truth. |
| `storage` | `internal/relocate/relocate.go`, surfaced by `internal/api/storage.go` | Storage root change or migration status. |
| `convert` | `internal/convert/convert.go` | Conversion result activity. |
| `jellyfin` | `internal/jellyfin/handoff.go` | Failed library scan notification. |
| provider-defined / test categories | `internal/api/server.go` and test fixtures may insert arbitrary category strings | The stream mirrors stored `core.Event.Category`; it does not invent a category registry or expose a new payload contract. |

All production writes flow to `Store.InsertEvent`; `internal/api/releases.go` also centralizes API-side writes through `server.logEvent`. `internal/store/events.go` orders rows by monotonic `id` descending. The event row has no owner field.

### Durable jobs and state transitions

The durable kinds currently relevant to screens and workers are `rss_sync`, `backlog_sweep`, `search_movie`, `search_episode`, `refresh_metadata`, `sync_site`, `import`, `convert`, `jellyfin_scan`, `storage_migrate`, `stash_scan`, and `stash_identify`. Some scheduling paths pass a library or module-specific kind string through the same queue; the stream must treat `Job.Kind` as an opaque bounded label rather than maintain a duplicate kind list.

`internal/core/download.go` defines the states `pending`, `running`, `done`, and `failed`. `internal/store/jobs.go` owns enqueue, claim/lease, completion, retry/backoff, and final failure transitions. Jobs are durable and replayable through `GET /jobs`, but they currently have no event cursor and must not be inferred from an activity hint.

### Consumers and polling cadence

| Consumer | Resource and cadence | Required fallback behavior |
|---|---|---|
| `web/src/lib/routes/History.svelte` | Active Events or Jobs tab; `GET /events` or `GET /jobs` every `10s`; paged “Load older” uses REST cursors. | Merge bounded REST pages by ID. A stream may prompt an immediate refresh but never owns rows. |
| `web/src/lib/state/downloads.svelte.ts` | Subscriber-selected fastest interval: queue `3000 ms`, badge `15000 ms`; overlap is dropped; hidden tabs pause; visibility return refreshes. | `GET /downloads` remains authoritative, including all pages. |
| `web/src/lib/state/requests.svelte.ts` | Screen `15000 ms`, badge `60000 ms`; overlap is dropped; hidden tabs pause; visibility return refreshes. | `GET /requests` remains authoritative. |
| `web/src/lib/routes/AdultSite.svelte` and `web/src/lib/adult.ts` | Catalogue status every `3000 ms`; one final read after `cataloguing` changes from true to false. | Preserve the final-read rule so the last year written by a walk is visible. |
| `web/src/lib/api/client.ts` | Exposes `listEvents`/`listJobs` and the separate downloads/requests APIs; no `EventSource` exists today. | Any future client helper must be additive and retain polling paths. |

Requests and downloads are separate resources. Download progress is not durable activity. Catalogue status is derived from open jobs in `internal/api/adultsites.go`; it needs a final REST read even if an invalidation arrives.

## Proposed endpoint and authorization

Do not implement this endpoint in the spike. If Outcome B is approved for production, use one endpoint:

```text
GET /api/v1/events/stream
```

It must be mounted inside the existing API subtree and pass through the same `requireSameOrigin` and `requireAuth` wrappers as `/events` and `/jobs`. The current declarative route policy leaves activity routes admin-only when accounts exist because `GET /events` and `GET /jobs` are absent from `memberAllowed`. Keep that policy unless a separate product decision adds ownership/redaction.

Required response headers:

```text
Cache-Control: no-cache
Content-Type: text/event-stream
X-Accel-Buffering: no
```

Credential paths are exactly the existing paths:

- session cookie issued by login;
- `X-Api-Key` for external clients;
- no query-string API key. `auth.go` reserves query keys for iCal, and the harness rejects one with `400`.

Expected failures are the existing API semantics: `401` when no valid credential is present, `403` when a valid member reaches the admin-only route, and the normal same-origin rejection before handler work. No new `authExempt` entry is required. Login/logout, iCal, and image delivery remain the only exemptions; an SSE stream must not be reachable by televisions or unauthenticated browsers.

Use a heartbeat comment every `20 seconds` and a maximum connection lifetime of `15 minutes`. Clients reconnect with jitter and continue their existing polling until the stream is confirmed healthy. The stream is read-only: no client command, acknowledgement, mutation, or durable write is allowed.

## Replay and event taxonomy

Use the monotonic event row ID as the durable activity `id` and `Last-Event-ID` replay cursor. A future implementation may accept an equivalent `since` parameter only if it has the same bounded semantics; `Last-Event-ID` is the canonical browser transport.

The server retains at most `32` replayable activity events in process memory for connected clients. It does not query unbounded history for a stream. If a cursor is missing, malformed, duplicated, or older than the retained window, emit one `reset` marker and close or require a bounded REST snapshot. The client then calls the existing paged GET endpoint and reconnects with the newest observed event ID. A duplicate cursor is harmless and must not duplicate a row in the client because REST merges by ID.

| Event name | ID / cursor behavior | Payload bound | Producer | Consumer | Fallback snapshot |
|---|---|---|---|---|---|
| `activity` | Durable event row `id`; replay only while retained or by bounded store pages | At most `4096` bytes after framing; mirror `core.Event` level/category/message/detail and optional IDs; never credentials or external paths outside current policy | Existing `InsertEvent` writers listed above | History activity view; an optional hint may refresh related screens | `GET /events?limit=100[&cursor=...]` |
| `invalidate` | Non-authoritative hint. It may carry a sequence for diagnostics but is not a replay cursor | Small fixed resource key: `downloads`, `requests`, `jobs`, `events`, `library`, or `catalogue:<site-id>`; no row or state payload | Approved write sites only; each producer must document resource and authorization | Existing REST store/state loaders | `GET /downloads`, `/requests`, `/jobs`, `/events`, library endpoint, or site detail endpoint |
| `reset` | No durable ID; tells the client its cursor is unusable | Fixed marker such as `snapshot-required` | Stream replay layer | Reconnect logic | The bounded snapshot named by the preceding resource |
| `heartbeat` | No ID and no state | Comment only, no payload | Stream connection timer | Connection health/reconnect logic | Existing polling; no snapshot needed |

`activity` mirrors history. `invalidate` only says “read this resource again.” Neither is an alternate authoritative state model. No member-specific activity may be streamed: current event/job rows have no owner field and both feeds are admin-only. A future member stream requires an explicit ownership or redaction design before a route policy change.

## Backpressure, lifecycle, and failure decisions

Use a bounded per-connection channel of `8` messages. Publishing must use a non-blocking send with a `0 ms` enqueue timeout. An invalidation is coalescible: if a subscriber is slow, drop it and rely on the next hint or the REST snapshot. A durable `activity` event is never allowed to block the writer; if it is not delivered live, replay it from the bounded event cursor or let the client perform a bounded `/events` snapshot. Do not put durable event data in an unbounded queue.

Bounds and decisions:

- maximum `32` replay events per connection;
- maximum `4096` bytes per framed event; reject or truncate at the producer boundary rather than allocate an unbounded payload;
- heartbeat every `20 s`;
- maximum connection lifetime `15 min`;
- maximum active connections is a deployment limit to set before launch; start with `100` and expose an ordinary `503` when full rather than growing memory;
- writer enqueue timeout `0 ms`; no durable transaction waits for a client;
- reconnect backoff `250 ms` to `30 s` with jitter; keep REST polling active during reconnect;
- disconnect removes the subscriber and closes its channel; no goroutine may retain a request, response writer, or payload after cancellation;
- process shutdown stops accepting streams, closes connections, and lets durable writes finish; no event is lost from the store because live delivery is only a hint;
- database unavailable means stream heartbeats may continue briefly, but activity replay returns `reset`/`503` and the client uses bounded REST retry; never fabricate state;
- reconnect storms are absorbed by the connection cap and jittered client backoff; do not add a broker for this spike.

The harness exercised a slow reader, disconnect/reconnect, replay, old cursor reset, and a forced snapshot. Its fixed channel dropped `24` hints under load without blocking the publisher.

## Prototype and reproducibility

`tmp/activity-stream-spike/main.go` is disposable and ignored by Git. It contains a minimal publisher and `httptest.Server`; it does not import a production package, register a production route, or alter a schema. It checks:

- zero-event heartbeat;
- one-event replay with `Last-Event-ID`;
- old cursor reset and bounded snapshot fallback;
- slow reader and channel drop behavior;
- publisher burst rate;
- authentication probes for no credential, member cookie, admin cookie, admin API key, and query API key;
- bounded event framing and replay capacity.

Run it with:

```sh
go run ./tmp/activity-stream-spike
```

It exits non-zero on a failed assertion and writes timestamped rows to `tmp/activity-stream-spike/measurements.tsv` plus `tmp/activity-stream-spike/summary.txt`. The recorded raw output is intentionally not committed because `tmp/` is ignored. The design and this result summary are the review artifacts.

## Verification and non-goals

The spike keeps these invariants:

- no production SSE endpoint;
- no `/events`, `/jobs`, `/downloads`, or `/requests` contract change;
- no durable schema or alternate client-side state model;
- no frontend poller replacement;
- REST snapshots remain authoritative;
- admin-only activity authorization and query-key restrictions remain unchanged.

Verification performed for this spike:

```text
go test -count=1 ./internal/api                 PASS
go test -count=1 ./internal/store               PASS
go test -count=1 ./internal/api ./internal/store PASS
(cd web && npm run check && npm test)            PASS
go test -count=1 ./... && go vet ./...           PASS
go run ./tmp/activity-stream-spike               PASS
```

The full repository and frontend checks were run against the worktree state that precedes this document; plan 016 adds no production source. The final design decision is therefore narrow: retain polling now, and only prototype a bounded read-only stream later if production-like measurements and an explicit authorization review continue to support Outcome B.
