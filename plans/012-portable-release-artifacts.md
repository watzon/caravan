# Plan 012: Package portable release artifacts

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected result before moving to the next step. If anything in STOP conditions occurs, stop and report; do not improvise. When done, update this plan's status row in `plans/README.md`.
>
> **Drift check (run first)**: `git diff --stat e4426f9..HEAD -- .github/workflows/ci.yml cmd/caravan/main.go cmd/caravan/prepare.go internal/prepare/binaries.go internal/prepare/binaries_test.go docs/portable.md compose.yaml Dockerfile`

## Status

- **Priority**: P3
- **Effort**: L
- **Risk**: MED
- **Depends on**: `plans/002-embedded-spa-freshness.md`, `plans/011-ci-contract-hardening.md`
- **Category**: migration
- **Planned at**: commit `e4426f9`, 2026-08-04

## Why this matters

The repository already declares five portable targets and `caravan prepare` can copy sibling binaries, but CI currently discards every cross-compiled executable after `go build ./...`. A release needs reproducible versioned binaries, checksums, uploaded artifacts, and a tagged-release bundle that `prepare -bin-dir` can consume. Hardware behavior on exFAT and real televisions cannot be faked in Actions, so it must be a repeatable manual evidence-recording process instead of an invented automated test.

## Current state

- `.github/workflows/ci.yml:69-100` defines the five targets and sets `CGO_ENABLED=0`, `GOOS`, and `GOARCH`, but only compiles packages and emits no binary, archive, checksum, or artifact.
- `cmd/caravan/main.go:12-14` defines `var version = "dev"`, with a documented `-ldflags "-X main.version=v1.2.3"` override; `main.go:41-48` dispatches `version` and prints `caravan dev` by default.
- `cmd/caravan/prepare.go:16-43` accepts `prepare drive-path`, `-force`, `-bin-dir`, and `-include-adult`; `-bin-dir` is documented as a directory holding sibling release builds.
- `internal/prepare/binaries.go:12-30` declares the same five `Targets`; lines 46-60 accept candidate layouts such as `linux-amd64/caravan`, `linux_amd64/caravan`, and `caravan-linux-amd64`. Lines 69-115 copy the host binary and found siblings, recording missing targets rather than failing.
- `internal/prepare/binaries.go:133-163` preserves an existing target unless `--force` and writes atomically. `internal/prepare/binaries_test.go` is the copy/layout test exemplar.
- `docs/SPEC.md:63-101` specifies the portable layout (`bin/darwin-arm64/caravan`, etc.), drive-relative config, launchers, and a release artifact or offline local directory as the source for other OS builds.
- `docs/PLAN.md:119-137` calls `prepare` and the real exFAT/TV test a phase-5 deliverable, while `docs/portable.md:217-280` contains the manual verification procedure and its pass/fail recording fields.
- `Dockerfile:1-80` is a source build image, not a release artifact source; do not make the release workflow depend on Docker.
### Exact excerpts

```go
// cmd/caravan/main.go:12-14
// version is the build version, overridden at release time with
// -ldflags "-X main.version=v1.2.3".
var version = "dev"
```

```go
// internal/prepare/binaries.go:19-30
var Targets = []Target{
    {GOOS: "linux", GOARCH: "amd64"},
    {GOOS: "linux", GOARCH: "arm64"},
    {GOOS: "windows", GOARCH: "amd64"},
    {GOOS: "darwin", GOARCH: "amd64"},
    {GOOS: "darwin", GOARCH: "arm64"},
}
```

```go
// cmd/caravan/prepare.go:21-23
binDir := fs.String("bin-dir", "",
    "directory holding release builds for the other operating systems "+
        "(default: next to this binary)")
```

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Build one versioned target | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-X main.version=v0.0.0" -o dist/caravan-v0.0.0-linux-amd64 ./cmd/caravan` | exit 0; executable exists and `version` prints `v0.0.0` |
| Package artifact | `tar -C stage/linux-amd64 -czf dist/caravan-v0.0.0-linux-amd64.tar.gz caravan` | exit 0; archive contains `caravan` |
| Checksums | `shasum -a 256 dist/*` | one stable checksum line per archive/binary |
| Prepare smoke | `caravan prepare "$TMPDIR/drive" -bin-dir "$TMPDIR/release-bins"` | exit 0; report lists host and sibling placements; no missing targets when staging is complete |
| Go prepare tests | `go test -count=1 ./internal/prepare ./cmd/caravan` | exit 0; layout, force, missing, and CLI tests pass |
| Canonical Go checks | `go test -count=1 ./...` and `go vet ./...` | exit 0 |

## Scope

**In scope (the only files to modify):**

- `.github/workflows/ci.yml` (cross-compile outputs/artifacts, if retained there)
- A new release workflow under `.github/workflows/` (tag trigger, packaging, checksums, upload)
- `internal/prepare/binaries.go` and `internal/prepare/binaries_test.go` only if release archive extraction requires a documented candidate spelling or a regression test
- `cmd/caravan/prepare.go` and its tests only if the smoke exposes a CLI contract gap
- `docs/portable.md` for the evidence-recording process and artifact naming

**Out of scope:**

- `plans/002-embedded-spa-freshness.md`; use its freshness output and do not duplicate it.
- Changes to download, storage, launcher behavior, or the portable database/integrity implementation.
- Automated claims that an exFAT filesystem or a model-specific television works.
- Source edits to create a second release matrix: `internal/prepare.Targets` remains the target list and workflow must test equality with it or keep one explicit synchronized list with a failing drift check.

## Git workflow

- Branch: `advisor/012-portable-release-artifacts`
- Commit style: conventional commits; use `ci: package versioned portable artifacts`.
- Do not push or open a PR.

## Steps

### Step 1: Define the artifact contract once

Choose and document the exact release names: `caravan-v0.0.0-linux-amd64.tar.gz` (with the analogous five target names) containing a binary named `caravan` (or `caravan.exe` for Windows), plus a sibling `.sha256` manifest covering the archives. Keep the extracted staging shape `release-bins/linux-amd64/caravan`, which `Target.candidates()` already accepts. Use `main.version` ldflags for the tag and reject a tag that is not the expected `vMAJOR.MINOR.PATCH` form.

**Verify**: extract one archive and run `./caravan version` → exit 0 and output exactly `caravan v0.0.0`.

### Step 2: Turn each matrix entry into a binary artifact

Refactor the existing cross-compile job or add a release workflow job so each target runs `go build` with `-trimpath`, `CGO_ENABLED=0`, the tag version, and a target-specific output path. Upload each archive as an Actions artifact for non-release validation where useful, and make the tagged workflow retain the same bytes for release packaging. Do not change the five target list without updating `internal/prepare.Targets` and its tests.

**Verify**: for all five targets, `file`/archive inspection finds one executable at the expected path and no target is silently omitted.

### Step 3: Generate checksums and tagged release assets

After all matrix jobs complete, download the five artifacts into one staging directory, generate SHA-256 checksums over the final archives in sorted filename order, and publish them to a GitHub release for the exact tag. Keep release creation idempotent: rerunning a tag must fail clearly or replace the same named assets rather than produce duplicate ambiguous files. Include a release summary naming target, archive, checksum, and embedded version.

**Verify**: `sha256sum -c caravan-v0.0.0.sha256` → every archive reports `OK`; `gh release view v0.0.0` → exactly five target archives and the checksum manifest are attached.

### Step 4: Exercise `prepare -bin-dir` against release-shaped inputs

In CI, define `drive="$RUNNER_TEMP/caravan-drive"` and `release_bins="$RUNNER_TEMP/release-bins"`, extract all five versioned archives into `"$release_bins/linux-amd64/caravan"`-style directories, build the current host binary, then invoke `caravan prepare "$drive" -bin-dir "$release_bins"`. Assert that all five `bin/linux-amd64/`, `bin/linux-arm64/`, `bin/windows-amd64/`, `bin/darwin-amd64/`, and `bin/darwin-arm64/` slots exist, each executable is non-empty, launchers are written, the config is drive-relative, and a second invocation without `-force` reports skipped binaries rather than rewriting them. Use `-include-adult` only in a separate assertion if the test needs to prove the explicit opt-in; the default smoke must prove no Adult root.

**Verify**: `go test -count=1 ./internal/prepare ./cmd/caravan` plus the temporary-drive command → exit 0; no `Missing` targets and all five named paths are present.

### Step 5: Record the manual exFAT/real-TV matrix

Update `docs/portable.md` with an evidence table keyed by date, Caravan version/checksum, drive model/capacity, filesystem/partition scheme, host OS/architecture, launcher, TV make/model/firmware, and results. The operator must format or inspect a real exFAT drive, run `prepare`, launch on at least two host OSes, safely shut down/eject, attach the drive to the documented TV, browse Movies/TV, and play representative files. Record commands, observed output, and pass/fail for every cell. Mark untested cells as untested; never turn a claimed matrix into an Actions emulation.

**Verify**: a reviewer can follow the documented procedure with a real drive and find a dated result record (or an explicit `NOT RUN` record) for every required matrix cell.

### Step 6: Validate release and repository contracts

Run plan 002's freshness check before any release build, then the canonical Go checks and prepare tests. Inspect workflow permissions: pull requests may upload temporary artifacts, but only a trusted tag workflow may create a release. Confirm no credentials or release tokens are printed.

**Verify**: `go test -count=1 ./... && go vet ./... && git diff --check` → exit 0; only in-scope files changed.

## Test plan

- Extend `internal/prepare/binaries_test.go` to cover the exact extracted release layout and Windows `.exe` naming; use existing `Targets`, `candidates`, and atomic-copy tests as the structural pattern.
- Extend `cmd/caravan/prepare_test.go` for `-bin-dir`, missing-target reporting, and the second-run skip behavior if CLI coverage is absent.
- Add a workflow smoke that invokes `version` from one artifact and the five-target `prepare -bin-dir` path; it must not attempt a real exFAT mount.
- Verification: `go test -count=1 ./internal/prepare ./cmd/caravan` and then `go test -count=1 ./...`.

## Done criteria

- [ ] Five targets produce versioned, reproducible binaries with the tag embedded in `caravan version`.
- [ ] Every final archive has a SHA-256 checksum; tagged releases upload all five archives and one manifest.
- [ ] Extracted release artifacts are accepted by `caravan prepare -bin-dir` and place all five slots in the documented drive layout.
- [ ] Release packaging depends on plan 002's freshness contract and contains no duplicate freshness implementation.
- [ ] `docs/portable.md` records manual exFAT/real-TV evidence without claiming automation.
- [ ] `go test -count=1 ./...` and `go vet ./...` pass; no source behavior changed outside Scope.

## STOP conditions

- A target in the workflow differs from `internal/prepare.Targets` and the mismatch cannot be resolved without a product decision.
- Release archives cannot be made to fit `Target.candidates()` without changing launcher or portable layout behavior.
- The tag has no trustworthy version or an archive checksum changes between identical builds without an approved reproducibility explanation.
- GitHub token permissions would allow pull requests to publish releases.
- Anyone proposes reporting exFAT or TV compatibility from a Linux CI runner instead of a dated hardware record.

## Maintenance notes

- Add a target in exactly two places only with a test that fails when the workflow matrix and `prepare.Targets` diverge.
- Keep archive names stable; `prepare` is intentionally liberal about unpacked directory names, but release links and checksum manifests are user-facing contracts.
- A release build must always run the embedded SPA freshness plan first; a valid binary with stale `web/dist` is not a valid release.
- Hardware evidence expires when the launcher, filesystem path handling, or media-serving behavior changes; mark old rows superseded rather than editing history.
