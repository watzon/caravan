# Plan 011: Harden targeted CI contracts

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected result before moving to the next step. If anything in the STOP conditions section occurs, stop and report; do not improvise. When done, update this plan's status row in `plans/README.md`.
>
> **Drift check (run first)**: `git diff --stat e4426f9..HEAD -- .github/workflows/ci.yml web/package.json web/vite.config.ts web/embed.go web/embed_test.go cmd/caravan/smoke_test.go cmd/caravan/smoke_usenet_test.go go.mod`

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: MED
- **Depends on**: `plans/002-embedded-spa-freshness.md`
- **Category**: tests
- **Planned at**: commit `e4426f9`, 2026-08-04

## Why this matters

The current workflow proves formatting, vet, the complete Go test suite, a build, the frontend checks/build, and that each target can compile, but it does not explicitly inspect dependency vulnerabilities, race-sensitive packages, frontend stderr quality, or a runnable binary's embedded SPA. These are separate contracts: a green compile can still hide a race, a Svelte warning, or an artifact that starts without its UI. Add small targeted gates without reimplementing plan 002's embedded-artifact freshness check.

## Current state

- `.github/workflows/ci.yml:22-41` runs `gofmt`, `go vet ./...`, `go test -count=1 ./...`, and `go build ./...` in one Go job.
- `.github/workflows/ci.yml:43-67` runs `npm ci`, `npm run check`, `npm test`, and `npm run build` in `web/`, but has no stderr policy.
- `.github/workflows/ci.yml:69-100` has the five-target matrix (`linux/amd64`, `linux/arm64`, `windows/amd64`, `darwin/amd64`, `darwin/arm64`) and only runs `go build ./...` with `CGO_ENABLED=0`; it does not retain or execute a binary.
- `.github/workflows/ci.yml:102-119` builds `caravan:ci` and runs `docker run --rm caravan:ci version`, which is a useful image smoke but not a direct host-binary/embedded-SPA smoke.
- `go.mod:1-13` is module `github.com/watzon/caravan`, Go `1.26.5`, and has no govulncheck dependency.
- `web/package.json:7-12` defines `npm run check`, `npm test`, and `npm run build`; `web/vite.config.ts:22-25` makes Vitest run `src/**/*.test.ts` under jsdom.
- `web/embed.go:14-30` embeds `all:dist` and exposes `DistFS()`; `web/embed_test.go:9-26` proves `dist/index.html` is present and reachable without the `dist/` prefix.
- `cmd/caravan/smoke_test.go:29-66` is the existing real acquisition smoke and is skipped with `-short`; `cmd/caravan/smoke_usenet_test.go:1-20` is the corresponding Usenet smoke. Do not make either an unconditional CI job.
- The existing frontend tests intentionally use Svelte components and API stubs (for example `web/src/lib/routes/AdultSite.test.ts:511-624`); match that convention rather than adding snapshot-only assertions.
### Exact excerpts

```yaml
# .github/workflows/ci.yml:33-40
- name: vet
  run: go vet ./...
- name: test
  run: go test -count=1 ./...
- name: build
  run: go build ./...
```

```yaml
# .github/workflows/ci.yml:93-100
env:
  CGO_ENABLED: "0"
  GOOS: ${{ matrix.goos }}
  GOARCH: ${{ matrix.goarch }}
run: go build ./...
```

```go
// web/embed.go:17-23
//go:embed all:dist
var Dist embed.FS

func DistFS() fs.FS {
    sub, err := fs.Sub(Dist, "dist")
```

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Go baseline | `go test -count=1 ./...` | exit 0; all packages pass |
| Vet baseline | `go vet ./...` | exit 0; no diagnostics |
| Vulnerability scan | `govulncheck ./...` | exit 0; no vulnerabilities reachable from this module (or a reviewed, pinned allowlist is documented) |
| Focused race test | `go test -race -short -count=1 ./internal/api ./internal/automation ./internal/download ./internal/store ./cmd/caravan` | exit 0; no race reports |
| Frontend typecheck | `(cd web && npm run check)` | exit 0; no TypeScript diagnostics |
| Frontend tests | `(cd web && npm test)` | exit 0; no unexpected stderr after cleanup |
| Frontend build | `(cd web && npm run build)` | exit 0; `web/dist/index.html` exists |
| Embedded package test | `go test -count=1 ./web` | exit 0; both `DistFS` tests pass |

## Suggested executor toolkit

- Use the `agent-ci` skill if available to inspect the workflow locally; this plan is specifically about GitHub Actions behavior.
- Keep plan 002's artifact-freshness implementation as the single freshness authority. Do not add a second generated-file hash/check job here.

## Scope

**In scope (the only files to modify):**

- `.github/workflows/ci.yml`
- `web/package.json` and, only if needed to make warning output deterministic, one small frontend test/config file under `web/`
- `cmd/caravan/smoke_test.go` or a new focused test beside it only if the embedded-binary smoke cannot be expressed in the workflow
- `go.mod`/`go.sum` only if the chosen govulncheck integration requires a pinned module tool (prefer an action/tool install that does not add runtime dependencies)

**Out of scope:**

- `plans/002-embedded-spa-freshness.md` and any duplicate artifact freshness check
- Product code, API behavior, dependency upgrades motivated only by a vulnerability report
- Making the long BitTorrent or Usenet transfer smoke mandatory on every pull request
- Any blanket `go test -race ./...` job if it makes the workflow unbounded; the requested contract is focused race coverage

## Git workflow

- Branch: `advisor/011-ci-contract-hardening`
- Commit style: conventional commits, matching `e4426f9` (`fix: the torrent engine's Close waits for its metadata watchers`); use `ci: harden targeted CI contracts` for the logical change.
- Do not push or open a PR.

## Steps

### Step 1: Establish the baseline output

Run the commands in the table above locally and capture the exact `govulncheck` availability and frontend stderr. Run `npm test` with stderr captured separately so the executor can distinguish Svelte compiler warnings from a test failure. Record package names that can execute goroutines in the focused race set; do not claim a warning is harmless without naming its source.

**Verify**: `go test -count=1 ./... && go vet ./... && (cd web && npm run check && npm test && npm run build)` → all commands exit 0; the output/stderr files identify any warning text to clean.

### Step 2: Add the vulnerability gate

Add a CI step that installs or invokes a pinned govulncheck tool compatible with `go.mod`'s Go 1.26.5, then runs `govulncheck ./...`. Keep tool installation separate from application dependencies. A vulnerability finding is not fixed by suppressing it: record the module, symbol, reachable path, and remediation in the job output, then STOP for maintainer review unless the finding is already addressed by a dependency change in scope.

**Verify**: `govulncheck ./...` → exit 0 and no unreviewed reachable vulnerability.

### Step 3: Add focused race coverage

Add a separate CI step/job for `go test -race -short -count=1` over `./internal/api ./internal/automation ./internal/download ./internal/store ./cmd/caravan`. These are the packages containing HTTP/session concurrency, job runner work, engine coordination, persistence, and composition-root lifecycle tests. If baseline evidence shows a narrower package is needed, document the exact exclusion and why; do not silently remove a package to make CI green.

**Verify**: `go test -race -short -count=1 ./internal/api ./internal/automation ./internal/download ./internal/store ./cmd/caravan` → exit 0 with no `WARNING: DATA RACE` output.

### Step 4: Clean and gate frontend diagnostics

Use the captured baseline to remove warnings caused by Caravan's code/tests (unused Svelte exports, invalid event handlers, unhandled rejected promises, or test setup noise). Keep expected dependency startup output out of the gate only by an explicit, narrow line match documented beside the command. Make the CI command fail on any remaining unexpected stderr while preserving the canonical `npm run check`, `npm test`, and `npm run build` commands. Do not turn warnings into an opaque redirect.

**Verify**: `(cd web && npm run check && npm test 2>test-stderr.txt; status=$?; test $status -eq 0 && test ! -s test-stderr.txt)` → exit 0 and `test-stderr.txt` is empty (or only the documented, exact allowlisted diagnostic is present).

### Step 5: Add a direct embedded-binary smoke

After plan 002's freshness step has established that `web/dist` is current, build a host binary and run it. At minimum invoke `"$TMPDIR/caravan-smoke" version` and start it in a temporary config/storage root, fetch `/` and assert an HTML response containing the literal `<html`, then terminate it cleanly. Reuse the existing `cmd/caravan` test helpers if a Go test is the least fragile implementation; do not duplicate plan 002's freshness assertion. Ensure the smoke cannot use source files outside the embedded filesystem.

**Verify**: `go build -o "$TMPDIR/caravan-smoke" ./cmd/caravan && "$TMPDIR/caravan-smoke" version` → exit 0 and prints `caravan dev`; the temporary server request returns HTTP 200 HTML and clean shutdown exits 0.

### Step 6: Validate the workflow shape

Run the exact canonical checks and inspect the diff. Confirm the new gates are independent, the race job is explicit and uses `-short` only to skip the documented long transfer smokes, frontend stderr is actionable, and the existing Docker smoke remains unchanged. Verify plan 002 is referenced rather than copied.

**Verify**: `git diff --check && git status --short` → no whitespace errors; only listed in-scope files are modified.

## Test plan

- Add or extend a workflow-level embedded-binary smoke covering `version`, `/`, and clean process termination.
- Keep `web/embed_test.go` as the structural pattern for embedded-file assertions: it reads `DistFS()` rather than inspecting the source tree.
- Use `cmd/caravan/smoke_test.go`'s `startCaravan`/`waitFor` pattern if a Go smoke is required, but keep the real network transfer skipped.
- Verify with `go test -count=1 ./...`, the focused `-race` command, `go vet ./...`, `govulncheck ./...`, and all three frontend commands.

## Done criteria

- [ ] `govulncheck ./...` is a required CI gate with a pinned/reproducible tool invocation.
- [ ] Focused race coverage runs the five named packages and reports no races.
- [ ] Frontend checks/tests/build remain required and unexpected stderr fails the job after code-owned warnings are removed.
- [ ] A host-built binary proves `version` and serves embedded `/` HTML; this does not duplicate plan 002 freshness.
- [ ] Existing Docker and cross-compile coverage remain green.
- [ ] No source/API behavior changed and no file outside Scope is modified.

## STOP conditions

- The live output contains a reachable vulnerability and no already-approved remediation.
- Any race report is intermittent or requires changing synchronization semantics outside this plan.
- Frontend stderr comes from a third-party tool and cannot be narrowly classified without hiding real warnings.
- The binary smoke serves placeholder HTML because plan 002 has not run or its freshness contract is failing; stop rather than weakening the smoke.
- A requested CI gate requires changing product code, adding a new external service, or making long network-transfer tests mandatory.

## Maintenance notes

- Keep `plans/002-embedded-spa-freshness.md` as the only artifact-freshness implementation; future CI edits should call it out explicitly.
- Revisit the focused race package list whenever a new long-lived worker or shared state package is added.
- Update the stderr allowlist only with a versioned tool change and an example diagnostic in the workflow review.
- The embedded smoke tests startup and packaging, not release artifact naming; plan 012 owns release packaging.
