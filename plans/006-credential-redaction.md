# Plan 006: Redact stored credentials from API responses

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected result before moving to the next step. If anything in the **STOP conditions** section occurs, stop and report; do not improvise. Do not change any file outside the Scope list. When done, update this plan's status row in `plans/README.md`.
>
> **Drift check (run first)**: `git diff --stat e4426f9..HEAD -- internal/api/indexers.go internal/api/indexers_test.go internal/api/handoff.go internal/api/handoff_test.go internal/api/settings.go internal/api/api_test.go internal/api/credentials_test.go web/src/lib/api/types.ts web/src/lib/api/client.ts web/src/lib/components/IndexerSettings.svelte web/src/lib/components/IndexerSettings.test.ts web/src/lib/components/JellyfinSettings.svelte web/src/lib/components/JellyfinSettings.test.ts web/src/lib/routes/Settings.svelte web/src/lib/routes/Settings.test.ts`
> If any in-scope file changed since this plan was written, compare the excerpts below against the live code before proceeding; on a mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: HIGH
- **Depends on**: none
- **Category**: security
- **Planned at**: commit `e4426f9`, 2026-08-04

## Why this matters

Three GET surfaces currently return secrets that are stored in SQLite: indexer API keys, Jellyfin API keys, and the TMDB API key in the flat settings map. A browser cache, screenshot, frontend error report, or proxy can therefore retain credentials that the server should keep write-only. Download clients already implement the desired contract: omit the secret and return a boolean indicating whether one is stored; an omitted request credential keeps the stored value and an explicit empty string clears it. This plan applies the same contract to the three older surfaces without changing credential-test endpoints or logging behavior.

## Current state

Relevant files and verified behavior:

- `internal/api/indexers.go:12-38` defines the wire DTO and copies `core.IndexerConfig.APIKey` into every list/create/update response.
- `internal/api/indexers.go:41-55,88-95` defines `indexerRequest.APIKey` as a required-value string and `config()` trims it; PUT is a full replacement, so a blank body currently clears the key.
- `internal/api/indexers.go:98-164` uses `indexerDTO` for GET `/indexers`, POST `/indexers`, and PUT `/indexers/{id}`.
- `internal/api/handoff.go:14-26` defines `jellyfinJSON.APIKey string`, and `internal/api/handoff.go:71-79,81-107` returns it from GET and POST. `jellyfinRequest.APIKey` is also a string and `handleSetJellyfin` currently replaces all three values.
- `internal/api/settings.go:139-147` returns the complete visible flat settings map. `internal/api/settings.go:150-208` treats PUT `/settings` as a partial update: absent keys remain untouched, present values are written.
- `internal/api/downloadclients.go:27-36,45-58` is the exemplar: `HasPassword`/`HasAPIKey` booleans are returned and secret fields are absent.
- `internal/api/downloadclients.go:61-68,73-88,126-137` defines pointer request credentials where omitted or `null` keeps the stored value and `""` explicitly clears it.
- `internal/api/indexers_test.go:13-80` currently expects indexer create/list/update responses to contain `APIKey` values (`"k"`, `"k2"`).
- `internal/api/handoff_test.go:36-80` currently expects Jellyfin POST and GET to return `APIKey == "secret"`; `handoff_test.go:140-157` proves an empty test body falls back to the stored credential.
- `internal/api/api_test.go:468-496` currently expects GET/PUT settings to return `tmdb_api_key: "k"` and preserve it in the response after a partial update.
- `internal/api/credentials_test.go:193-205` proves POST metadata test with an empty body uses the stored TMDB key; the test endpoint must continue to do so.
- `web/src/lib/api/types.ts:468-481` models settings as `Record<string,string>` and names `tmdb_api_key`; `types.ts:576-586` models `JellyfinConfig.api_key` as a returned value; `types.ts:694-710` models `Indexer.api_key` as a round-tripping value.
- `web/src/lib/components/IndexerSettings.svelte:48-53,131-164` pre-fills `apiKey` from `indexer.api_key` and always sends `api_key`, including a blank string on an edit.
- `web/src/lib/components/JellyfinSettings.svelte:34-53,66-76` pre-fills `apiKey` from GET and always sends it on save.
- `web/src/lib/routes/Settings.svelte:213-233,243-255,395-429` pre-fills `tmdbKey` from `settings.tmdb_api_key` and saves it through the partial settings API. The existing Test button posts the field value to `/settings/metadata/test` and must remain write-only.
- `web/src/lib/components/DownloadClientSettings.svelte:11-13,122-164` is the frontend exemplar: credential inputs begin blank, `has_*` flags tell the user one is stored, and blank fields are omitted from save/test bodies.

The current API response shapes are therefore known: indexer rows have `api_key`, Jellyfin objects have `api_key`, and settings maps have `tmdb_api_key`. The replacement shapes must not carry those values.

The live DTO and exemplar are:

```go
// internal/api/indexers.go:19-38
type indexerJSON struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	APIKey     string `json:"api_key"`
	Type       string `json:"type"`
	Categories []int  `json:"categories"`
	Enabled    bool   `json:"enabled"`
}

func indexerDTO(c core.IndexerConfig) indexerJSON {
	return indexerJSON{
		ID:         c.ID,
		Name:       c.Name,
		URL:        c.URL,
		APIKey:     c.APIKey,
		Type:       c.Type,
		Categories: categoryList(c.Categories),
		Enabled:    c.Enabled,
	}
}
```

```go
// internal/api/downloadclients.go:27-36,65-88
type downloadClientJSON struct {
	ID         int64  `json:"id"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	Username   string `json:"username"`
	HasPassword bool  `json:"has_password"`
	HasAPIKey   bool  `json:"has_api_key"`
}

// Omitted (or null) keeps what is stored; "" clears it deliberately.
type downloadClientRequest struct {
	Password *string `json:"password"`
	APIKey   *string `json:"api_key"`
}
```

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Go API tests | `go test -count=1 ./internal/api` | exit 0; redaction, keep, clear, and existing API tests pass |
| Go store/API tests | `go test -count=1 ./...` | exit 0; all Go packages pass |
| Frontend checks | `cd web && npm run check` | exit 0, no TypeScript/Svelte diagnostics |
| Frontend tests | `cd web && npm test` | exit 0; updated API/component tests pass |
| Frontend build | `cd web && npm run build` | exit 0 |
| Secret scan in responses | `go test -count=1 ./internal/api -run 'Credential|Indexer|Jellyfin|Settings'` | exit 0 and tests assert raw bodies contain neither sentinel credential nor secret field names |

## Suggested executor toolkit

- Read `internal/api/downloadclients.go` and `web/src/lib/components/DownloadClientSettings.svelte` before implementing; match their `has_*`, pointer, and omission semantics rather than inventing a masked string such as `"****"`.
- Use the existing API test helpers in `internal/api/api_test.go` (`do`, `decodeBody`, `wantStatus`) and frontend fetch stubs in `IndexerSettings.test.ts`, `JellyfinSettings.test.ts`, and `Settings.test.ts`.

## Scope

**In scope (the only files to modify):**

- `internal/api/indexers.go`
- `internal/api/indexers_test.go`
- `internal/api/handoff.go`
- `internal/api/handoff_test.go`
- `internal/api/settings.go`
- `internal/api/api_test.go`
- `internal/api/credentials_test.go`
- `web/src/lib/api/types.ts`
- `web/src/lib/api/client.ts`
- `web/src/lib/components/IndexerSettings.svelte`
- `web/src/lib/components/IndexerSettings.test.ts`
- `web/src/lib/components/JellyfinSettings.svelte`
- `web/src/lib/components/JellyfinSettings.test.ts`
- `web/src/lib/routes/Settings.svelte`
- `web/src/lib/routes/Settings.test.ts`

**Out of scope (do not touch):**

- `internal/api/downloadclients.go` and its tests; this is the fixed exemplar, not a migration target.
- `internal/api/credentials.go` and metadata-test handler semantics; POST `/settings/metadata/test` continues to accept a candidate key and an empty body continues to test the stored key.
- `internal/store` schemas or credential storage; secrets remain stored and usable by backend clients.
- `internal/api/stash.go`/adult settings; they are a separate surface and no evidence in this assignment requires changing them.
- `web/src/lib/components/AdultSettings.svelte`, `StashSettings.svelte`, or unrelated settings panes.
- Any source, documentation, or plan file outside this list.

## Git workflow

- Branch: `advisor/006-credential-redaction`
- Commit style: conventional commits, matching the repository's recent `fix:`/`feat:` history. Use `fix: redact stored credentials from API responses`.
- Do not push or open a PR unless the operator instructs it.

## Steps

### Step 1: Define redacted wire contracts and request semantics

Update the Go DTOs and frontend types together:

1. Replace `indexerJSON.APIKey string` with `HasAPIKey bool json:"has_api_key"`; `indexerDTO` sets it from `c.APIKey != ""`. `api_key` must be absent from JSON, not present with an empty value.
2. Change `indexerRequest.APIKey` to `*string`. In `config`, `nil` means keep the stored value only on PUT; an explicit `""` means clear. Since `config` currently has no stored argument, add a focused merge step in the update handler (or an equivalent helper) that reads the existing row before validation/write. POST with nil has no stored row and therefore stores empty. Never copy a credential from an unrelated row.
3. Replace `jellyfinJSON.APIKey string` with `HasAPIKey bool json:"has_api_key"`; construct the DTO from the stored config without the key. Change `jellyfinRequest.APIKey` to `*string`; POST with nil keeps the stored key, explicit `""` clears it, and a non-empty value replaces it. Preserve the current empty-body test fallback in `handleTestJellyfin`; it is a candidate-probe endpoint, not a GET response.
4. Keep settings as a flat `map[string]string` to avoid an unrelated envelope migration. `visibleSettings` must remove `tmdb_api_key` and add `tmdb_api_key_set` with value `"true"` or `"false"` based on storage. This metadata flag is not the credential. PUT remains partial: absent `tmdb_api_key` keeps the value; present `""` clears it; a non-empty value replaces it. PUT responses must also omit the secret and report the current `tmdb_api_key_set` flag.
5. Update `JellyfinConfig` and `Indexer` types to use `has_api_key`; add optional write-only `api_key?: string` to `IndexerInput` and `JellyfinConfigInput`. Keep `Settings` string-valued and add a named `SETTING_TMDB_API_KEY_SET` constant. Do not add a masked credential value.

**Verify**: `go test -count=1 ./internal/api -run 'TestIndexerCRUD|TestJellyfinConfigRoundTrip|TestSettingsRoundTrip'` → expected failure only in old assertions that still expect returned secret values; no compile errors outside the deliberate contract assertions. Do not proceed until every compile error is understood.

### Step 2: Make backend writes preserve credentials without exposing them

Implement the merge and serialization paths in `internal/api/indexers.go`, `handoff.go`, and `settings.go`:

- Indexer update reads the existing row before constructing the final config, merges only a nil request key, validates the final config, and returns `indexerDTO`. Create treats nil as no credential. A rejected update must not change the stored key.
- Jellyfin POST reads the stored config before merging a nil API key. Continue validating URL/enabled before writing. Return the redacted DTO on success. Keep all credential use inside the server and never include the value in errors.
- `visibleSettings` must not mutate the store's map in a way that loses the secret on PUT; it only changes the response copy. Its flag must reflect an absent or empty value as false.

**Verify**: `go test -count=1 ./internal/api -run 'TestIndexerCRUD|TestJellyfinConfigRoundTrip|TestJellyfinTestConnection'` → exit 0 after tests are updated in Step 3; response structs contain `HasAPIKey`, never an API key string.

### Step 3: Add API regression tests for no leakage, keep, and clear

Update/add tests in the existing files:

- Indexers: create with sentinel `index-secret`; assert POST body does not contain the sentinel or `"api_key"`, `has_api_key` is true, GET also contains neither, PUT omitting `api_key` preserves the stored sentinel and reports true, PUT with `api_key:""` clears and reports false, and a rejected update leaves the original secret unchanged in the store.
- Jellyfin: create with sentinel `jelly-secret`; assert POST/GET omit the secret and field and report `has_api_key:true`; POST omitting `api_key` preserves it; POST with `api_key:""` clears it; the test endpoint still sends the stored key for `{}` and sends an explicitly supplied candidate for a non-empty body. Add the all-or-nothing write test described in Plan 008 only if it is not already covered there; otherwise do not duplicate it.
- Settings/TMDB: seed `tmdb-secret` directly, assert GET `/settings` and PUT responses contain no secret and no `"tmdb_api_key"` key, but contain `"tmdb_api_key_set":"true"`; assert a PUT unrelated key preserves the store value; assert `tmdb_api_key:""` clears it and returns `tmdb_api_key_set:"false"`; preserve the existing metadata-test test that an empty body uses the stored secret without echoing it.

Model the raw-body checks on `TestDownloadClientCredentialsAreNeverReturned` in `internal/api/downloadclients_test.go:137-199`: check both value and field-name absence, not merely a zero decoded field.

**Verify**: `go test -count=1 ./internal/api -run 'Test(Indexer|Jellyfin|Settings|Metadata)'` → exit 0; every sentinel remains in the store only where keep semantics require it and never in a response body.

### Step 4: Migrate frontend consumers to write-only fields

Update the frontend without ever copying a redacted field back into a request:

- `IndexerSettings.svelte`: start edit with `apiKey = ''`, retain `indexer.has_api_key`, display a stored-credential hint, omit `api_key` from the body when blank, and provide an explicit Clear action that sets `api_key: ''`. Keep category loading and unsaved category tests using the currently typed key.
- `JellyfinSettings.svelte`: start the API-key field blank after GET, retain `has_api_key`, show a stored hint, omit `api_key` from save when blank, and provide an explicit Clear action. Testing with a blank field must continue to send an empty body field only if the existing test contract requires it; the server's empty-body fallback remains authoritative.
- `Settings.svelte`: initialize the TMDB field blank when the response contains only `tmdb_api_key_set`; saving a non-empty key sends `tmdb_api_key`; saving unrelated settings does not send the key; add an explicit Clear control that sends `tmdb_api_key: ''`. The Test button continues to send the currently typed candidate and must never obtain a key from GET.
- Update `web/src/lib/api/client.ts` method signatures to separate redacted GET models from write request models; do not use `Omit` in a way that accidentally makes a secret required on GET.

**Verify**: `cd web && npm run check` → exit 0 with no missing `api_key`/`has_api_key` properties.

### Step 5: Update frontend contract tests and perform end-to-end verification

Update `IndexerSettings.test.ts`, `JellyfinSettings.test.ts`, and `Settings.test.ts` fixtures to use redacted responses. Add assertions that edit fields are blank, stored hints are visible, ordinary saves omit secret properties, typed values are sent, and explicit Clear sends an empty string. Add a raw fetch assertion where practical that redacted fixture bodies never include the sentinel.

**Verify**: `cd web && npm test` → exit 0; `cd web && npm run build` → exit 0; then `go test -count=1 ./...` → exit 0.

## Test plan

- Go API regression tests in `internal/api/indexers_test.go`, `handoff_test.go`, `api_test.go`, and `credentials_test.go`; model raw response checks on `internal/api/downloadclients_test.go:137-199`.
- Frontend component contract tests in `IndexerSettings.test.ts`, `JellyfinSettings.test.ts`, and `Settings.test.ts`; retain existing category/test-button coverage.
- Cases required: absent credential, stored credential, ordinary edit with omitted credential (keep), explicit empty credential (clear), candidate test with unsaved key, rejected write with no mutation, and no secret value or secret field in GET/POST/PUT response bodies.
- Verification: `go test -count=1 ./...`, `cd web && npm run check`, `cd web && npm test`, and `cd web && npm run build` all exit 0.

## Done criteria

- [ ] GET `/indexers`, indexer POST/PUT responses contain `has_api_key` and never contain `api_key` or its stored value.
- [ ] GET/POST `/handoff/jellyfin` responses contain `has_api_key` and never contain `api_key` or its stored value.
- [ ] GET/PUT `/settings` omit `tmdb_api_key`, expose only `tmdb_api_key_set`, and never echo the stored value.
- [ ] Indexer and Jellyfin omitted/null credentials keep the stored value; explicit `""` clears it; TMDB PUT omission keeps and explicit empty clears.
- [ ] Metadata, Jellyfin, and indexer test endpoints still use candidate-or-stored fallback without returning credentials.
- [ ] `go test -count=1 ./...` exits 0.
- [ ] `cd web && npm run check`, `npm test`, and `npm run build` exit 0.
- [ ] `git status --short` shows modifications only in this plan's Scope list.
- [ ] No source, response, log, or error path contains a masked or real credential value.

## STOP conditions

Stop and report if:

- Any current DTO, route, or frontend fixture differs from the excerpts or no longer has the named endpoint.
- A response contract cannot omit `tmdb_api_key` while preserving the flat settings map and the existing partial-update behavior without touching an out-of-scope caller.
- A caller requires the real credential in a GET response rather than accepting the `has_*`/write-only contract; do not add a compatibility echo.
- The executor cannot distinguish omitted/null from explicit empty for JSON decoding; do not approximate with a plain string.
- A failed write can leave indexer/Jellyfin/TMDB settings partially changed after the proposed merge; stop before weakening atomicity.
- Any test or error response contains a sentinel credential after the change.
- Frontend tests or type checks require an unscoped route/component change.

## Maintenance notes

- Reviewers should scrutinize every JSON DTO and raw-body assertion; a secret accidentally reintroduced as an embedded core struct field will bypass the intended contract.
- New credential-bearing integrations should copy `downloadClientDTO`/`downloadClientRequest` semantics, including an explicit stored boolean and omitted/null-versus-empty behavior.
- Do not log request bodies or URLs while changing these handlers. Candidate test values remain request-only and must never be placed in response errors.
- The TMDB credential health state in `/system/status` remains the user-facing verdict; it is intentionally not a credential value and should not be replaced with a masked key.
