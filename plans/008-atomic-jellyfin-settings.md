# Plan 008: Commit Jellyfin settings atomically

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected result before moving to the next step. If anything in the **STOP conditions** section occurs, stop and report; do not improvise. Do not change any file outside the Scope list. When done, update this plan's status row in `plans/README.md`.
>
> **Drift check (run first)**: `git diff --stat e4426f9..HEAD -- internal/api/handoff.go internal/api/handoff_test.go internal/store/settings.go internal/store/settings_test.go`
> If any in-scope file changed since this plan was written, compare the excerpts below against the live code before proceeding; on a mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: S
- **Risk**: HIGH
- **Depends on**: `plans/006-credential-redaction.md`
- **Category**: bug
- **Planned at**: commit `e4426f9`, 2026-08-04

## Why this matters

Jellyfin URL, API key, and enabled state describe one validated handoff, but `POST /handoff/jellyfin` currently writes them with three independent `SetSetting` calls. A disk, lock, or SQLite failure after the first write can leave a new URL beside an old key or enabled flag, making a configuration that was never tested. The store already provides `SetSettings`, a transaction that commits every pair or none, and its test deliberately injects a failure between sorted writes. Use that existing pattern at the API boundary and add an API-level failure test so the endpoint cannot report a partial configuration.

## Current state

- `internal/api/handoff.go:14-26` defines `jellyfinJSON` with URL, API key, and enabled fields.
- `internal/api/handoff.go:35-46` defines the request shape and `internal/api/handoff.go:48-68` validates URL and the enabled-without-URL rule.
- `internal/api/handoff.go:71-79` reads the stored config for GET.
- `internal/api/handoff.go:81-107` documents a replace operation, builds a three-key map at lines 95-99, then loops over `[SettingJellyfinAPIKey, SettingJellyfinEnabled, SettingJellyfinURL]` and calls `s.st.SetSetting` once per key. The sorted order is deterministic but not atomic.
- `internal/store/settings.go:178-187` defines `SetSetting` as one independent UPSERT.
- `internal/store/settings.go:190-228` defines `SetSettings`: it starts a transaction, sorts keys, executes all UPSERTs, rolls back on any error, commits once, and returns an error otherwise.
- `internal/store/settings_test.go:8-27` proves `SetSettings` writes both keys; `settings_test.go:29-71` creates a trigger that refuses the second sorted update and asserts the old pair remains intact.
- `internal/api/handoff_test.go:36-80` exercises Jellyfin round-trip and verifies all three stored keys; `handoff_test.go:82-113` covers validation and disabled configurations; `handoff_test.go:140-157` covers test fallback to stored credentials.
- `internal/api/api_test.go:333-345` shows that API tests use a real temporary `*store.Store`, so a SQLite trigger can inject the failure without a test-only store interface.

The required implementation is a one-line behavioral substitution in the existing handler: call `s.st.SetSettings(r.Context(), values)` once, preserve the same error envelope and response, and leave `SetSettings` itself unchanged.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Store transaction tests | `go test -count=1 ./internal/store -run TestSetSettings` | exit 0; both write and all-or-nothing tests pass |
| API handoff tests | `go test -count=1 ./internal/api -run 'TestJellyfin'` | exit 0; round-trip, validation, fallback, and failure atomicity pass |
| Full Go tests | `go test -count=1 ./...` | exit 0 |
| Static checks | `go vet ./...` | exit 0 |

## Suggested executor toolkit

- Reuse the exact SQLite trigger technique in `internal/store/settings_test.go:44-53`; do not add a mock database or alter production transaction code.
- Keep the existing sorted key order in `SetSettings`. The transaction already owns ordering and rollback.

## Scope

**In scope (the only files to modify):**

- `internal/api/handoff.go`
- `internal/api/handoff_test.go`
- `internal/store/settings.go` only if a narrow regression fix is required by an existing test; the intended change does not require it.
- `internal/store/settings_test.go` only if the existing failure test needs a precise extension; prefer leaving it unchanged.

**Out of scope (do not touch):**

- Jellyfin client/network code and test endpoint behavior.
- `internal/api/settings.go`, which writes unrelated independent settings and has different partial-update semantics.
- `internal/api/stash.go`; it already uses `SetSettings` and is the closest endpoint pattern.
- Database migrations, schema changes, frontend files, and plans README/index files.
- Any source or documentation file outside this list.

## Git workflow

- Branch: `advisor/008-atomic-jellyfin-settings`
- Commit style: conventional commits. Use `fix: commit Jellyfin settings atomically`.
- Do not push or open a PR unless instructed.

## Steps

### Step 1: Switch the Jellyfin handler to the transactional store method

In `handleSetJellyfin`, preserve request decoding, `config()` validation, the exact three-key map, and the existing `writeStoreError` message. Replace the sorted loop and its three `SetSetting` calls with one `s.st.SetSettings(r.Context(), values)` call. Do not call `SetSetting` before or after it. The successful response remains `jellyfinJSON(cfg)` and the failed response remains the existing JSON store error.

**Verify**: `go test -count=1 ./internal/api -run 'TestJellyfinConfigRoundTrip|TestJellyfinConfigRejectsBadRequests|TestJellyfinConfigCanBeDisabled'` -> exit 0; valid and invalid configurations retain their existing status and response contracts.

### Step 2: Add API-level failure atomicity coverage

Extend `internal/api/handoff_test.go` with a test that uses `newTestServer` and a real store:

1. POST a valid old configuration with URL `http://old-jellyfin:8096`, API key `old-key`, and enabled true. Assert success.
2. Create a SQLite trigger on `settings` that aborts an UPDATE where `NEW.key = SettingJellyfinURL`, matching the existing store trigger pattern. The map's sorted order writes API key, enabled, then URL, so this fails after two attempted updates and exercises rollback.
3. POST a valid new configuration with URL `http://new-jellyfin:8096`, API key `new-key`, and enabled false. Assert a server error response with the existing `writeStoreError` JSON envelope.
4. GET `/handoff/jellyfin` and assert the old URL, old enabled value, and old key state remain. If the credential response is redacted by Plan 006 before this executes, assert the `has_api_key` flag and read the exact stored key directly from `st.GetSetting` only inside the test.
5. Drop the trigger or let the temporary database close at cleanup; do not alter production schema.

Keep the existing `TestSetSettingsIsAllOrNothing` store test as the lower-level proof. The new test proves the Jellyfin endpoint actually invokes the transactional path.

**Verify**: `go test -count=1 ./internal/api -run 'TestJellyfin.*Atomic|TestJellyfinConfig'` -> exit 0; the failed POST leaves all three old settings intact.

### Step 3: Recheck lower-level transaction behavior and all callers

Run the existing store tests and inspect `internal/api/stash.go:158-164` as the existing endpoint user of `SetSettings`. Do not change `SetSettings` sorting, transaction boundaries, or error wrapping unless an existing test fails for a real regression. Ensure the Jellyfin map has no extra keys and no key is silently omitted.

**Verify**: `go test -count=1 ./internal/store -run TestSetSettings` -> exit 0; `go test -count=1 ./internal/api -run 'Test(Jellyfin|Stash)'` -> exit 0.

### Step 4: Run canonical verification

Run the full Go suite and vet. This plan has no frontend changes.

**Verify**: `go test -count=1 ./...` -> exit 0; `go vet ./...` -> exit 0.

## Test plan

- API regression: `internal/api/handoff_test.go`, valid old configuration, failing new configuration, GET/store assertions after rollback.
- Store structural pattern: `internal/store/settings_test.go:29-71`, including sorted-key trigger and old-pair assertion.
- Existing coverage retained: round-trip, URL validation, enabled-without-URL rejection, disabled configuration, test candidate, stored fallback, and upstream failure reason.
- Verification: focused API/store tests, then `go test -count=1 ./...` and `go vet ./...` all exit 0.

## Done criteria

- [ ] `handleSetJellyfin` calls `SetSettings` exactly once for URL, API key, and enabled values.
- [ ] No Jellyfin handler path uses three independent `SetSetting` calls for one configuration.
- [ ] A failure during the third sorted write returns the existing error envelope and leaves all old settings intact.
- [ ] Existing Jellyfin GET, POST, test, validation, and disabled-state behavior remains covered and passing.
- [ ] `go test -count=1 ./...` exits 0.
- [ ] `go vet ./...` exits 0.
- [ ] `git status --short` shows modifications only in this plan's Scope list.

## STOP conditions

Stop and report if:

- The handler no longer has exactly the three Jellyfin setting keys or the store method no longer provides transactional rollback.
- A failure trigger cannot be installed against the temporary SQLite store, or the test cannot prove all three old values remain after a failed request.
- The new code changes response status, error text, credential fallback, or validation without a separate requirement.
- A production change appears necessary in `internal/store/settings.go` beyond the existing `SetSettings` contract; stop before replacing the transaction with a new abstraction.
- The endpoint still writes any of the three keys before the transaction begins.
- A test passes only because it checks one key while another key was partially updated.

## Maintenance notes

- Any future handoff with interdependent settings should use `SetSettings`, not a loop of `SetSetting` calls.
- Reviewers should inspect the trigger test's old/new values and confirm it fails after a prior write attempt, not before any work.
- Keep independent settings on `SetSetting`; broadening every PUT to a transaction would change unrelated partial-update semantics.
- Plan 006 may redact the Jellyfin key in the response. Its atomicity test must then use `has_api_key` plus a direct store assertion, never a response secret.
