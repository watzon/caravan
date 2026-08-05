# Plan 013: Reconcile product documentation

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected result before moving to the next step. If anything in STOP conditions occurs, stop and report; do not improvise. When done, update this plan's status row in `plans/README.md`.
>
> **Drift check (run first)**: `git diff --stat e4426f9..HEAD -- docs/SPEC.md docs/PLAN.md docs/docker.md README.md compose.yaml Dockerfile docs/portable.md`

## Status

- **Priority**: P3
- **Effort**: M
- **Risk**: LOW
- **Depends on**: none
- **Category**: docs
- **Planned at**: commit `e4426f9`, 2026-08-04

## Why this matters

The three operational documents describe different product moments. `SPEC.md` still calls multi-user/request management future, `PLAN.md` says those tracks shipped and labels the roadmap eleven phases while defining phase 12, and `docker.md` tells readers to pull an image even though the checked-in Compose file builds from source. Reconcile the facts and add a concise root README so a new operator can choose setup and verification without guessing which document wins.

## Current state

- `docs/SPEC.md:1-5` is Draft v0.2 dated 2026-07-31; `docs/SPEC.md:388` lists “multi-user/request management” as a post-v1 candidate, and `docs/SPEC.md:406-408` lists “Multi-user, request approval workflows” under explicit non-goals.
- `docs/PLAN.md:1-8` is v1.1 dated 2026-08-03 and says “Eleven phases.” `docs/PLAN.md:186-188` explicitly says discover/requests and multi-user RBAC shipped between revisions, while `docs/PLAN.md:288-307` defines and accepts Phase 12 Explore expansion.
- `docs/docker.md:11-17` quickstarts with `docker compose up -d`; `docs/docker.md:35-42` correctly says `compose.yaml` builds from the checkout. `docs/docker.md:175-183` instead tells an upgrader to run `docker compose pull` or `docker compose build --pull`.
- `compose.yaml:11-18` uses `image: caravan:latest` plus a `build: context: .` block, so a clean checkout's first `up` builds a local image; there is no registry image reference in the file.
- `docs/docker.md:217-232` honestly says end-to-end Docker volume/hardlink behavior is not yet verified and distinguishes CI image build from real-host evidence. Preserve that distinction.
- `docs/SPEC.md:31-65` describes Docker, bare binary, and portable disk as deployment modes; `docs/PLAN.md:119-137` makes these phase-5 deliverables and links manual Docker/portable procedures.
- There is no root `README.md` in the checkout. The README requested here must be concise and link to `docs/SPEC.md`, `docs/PLAN.md`, `docs/docker.md`, `docs/portable.md`, and the canonical verification commands; it must not duplicate the documents.
### Exact excerpts

```markdown
# docs/PLAN.md:1-8
**Status:** v1.1 (companion to `SPEC.md` Draft v0.2)

Eleven phases. Each absorbs one hat of the existing *arr ecosystem and ends
with a deliverable a real user can run and test.
```

```markdown
# docs/PLAN.md:186-188
Between v1.0 and v1.1, three unplanned tracks shipped and are treated as done:
**discover & requests**, **multi-user RBAC**, and **recurring metadata refresh**.
```

```yaml
# compose.yaml:11-17
services:
  caravan:
    image: caravan:latest
    build:
      context: .
```

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Locate contradictions | `grep -nE "Eleven phases|Phase 12|multi-user|request|docker compose pull|build" docs/SPEC.md docs/PLAN.md docs/docker.md compose.yaml` | every hit is classified as shipped, future, or deployment procedure |
| Go verification | `go test -count=1 ./...` | exit 0 |
| Vet | `go vet ./...` | exit 0 |
| Frontend verification | `(cd web && npm run check && npm test && npm run build)` | all three exit 0 |
| Link/path check | `test -f README.md && test -f docs/SPEC.md && test -f docs/PLAN.md && test -f docs/docker.md && test -f docs/portable.md` | exit 0 |

## Scope

**In scope (the only files to modify):**

- `docs/SPEC.md`
- `docs/PLAN.md`
- `docs/docker.md`
- New root `README.md`

**Out of scope:**

- `compose.yaml`, `Dockerfile`, workflows, or Go/Svelte source; document their current behavior rather than changing implementation.
- Rewriting the specification or roadmap wholesale.
- Any claim that Docker hardlink imports or portable hardware are verified beyond the evidence already recorded in `docs/docker.md` and `docs/portable.md`.
- Creating a second README under `docs/`.

## Git workflow

- Branch: `advisor/013-documentation-reconciliation`
- Commit style: conventional commits; use `docs: reconcile product documentation`.
- Do not push or open a PR.

## Steps

### Step 1: Build a contradiction ledger

Create a temporary ledger (not a committed extra file) with each contradictory sentence, its source line, the verified implementation fact, and the chosen canonical wording. At minimum include: (1) future multi-user/request claims versus PLAN's shipped interlude and current auth/request routes, (2) “Eleven phases” versus the existing Phase 12 section, and (3) Compose pull versus source build. Use the live repository, not memory, to classify a statement.

**Verify**: `grep -nE "post-v1|non-goals|Eleven phases|Interlude|Phase 12|docker compose pull|build:" docs/SPEC.md docs/PLAN.md docs/docker.md compose.yaml` → every result is assigned one canonical status or an explicit historical note.

### Step 2: Reconcile shipped versus future language

Make `SPEC.md` distinguish current shipped scope from post-v1 candidates. Preserve the product boundaries that are still true, but move the now-shipped discover/requests and multi-user RBAC statements into a “shipped in v1.1” or equivalent status note and leave genuinely future work (for example custom formats, music, mobile) future. Update `PLAN.md`'s phase-count wording to name the existing phase sequence accurately (including Phase 12), without pretending the interlude was part of the numbered phases. Keep route names and acceptance claims tied to implementation: current auth allowlists in `internal/api/auth.go` and request handlers in `internal/api/requests.go` are the source of truth for shipped status.

**Verify**: `grep -nE "multi-user|request|Eleven phases|Phase 12|post-v1" docs/SPEC.md docs/PLAN.md` → no sentence simultaneously calls a shipped track future or says there are eleven phases while presenting Phase 12 as active.

### Step 3: Reconcile Compose setup and upgrade instructions

In `docs/docker.md`, state clearly that the checked-in `compose.yaml` is a source-build checkout quickstart (`docker compose up -d` or `up -d --build`) and separately describe the future/published-image path only if a real image reference exists. Do not tell a checkout user that `docker compose pull` is the default when the file's `build:` block controls startup. If retaining `docker compose pull`, label it as an operator-selected published-image override and show the required compose change; otherwise remove it. Keep the manual verification status and exact hardlink/ownership procedure unchanged except for command corrections.

**Verify**: `grep -nE "docker compose (up|pull|build)|image:|build:" docs/docker.md compose.yaml` → each command matches the file it claims to operate on and source-build versus published-image paths are explicit.

### Step 4: Add the root README

Create `README.md` with a short product description, prerequisites, three setup choices (bare binary, Docker checkout, portable drive), first useful commands, canonical verification commands, and links to the four detailed docs plus the issue/plan context only if an existing link is real. Mention that Docker and real portable hardware have manual verification status rather than implying CI proves them. Do not copy the full storage, auth, or exFAT sections into the README.

**Verify**: `test -f README.md && grep -nE "SPEC.md|PLAN.md|docker.md|portable.md|go test -count=1 ./\.\.\.|go vet ./\.\.\." README.md` → all required links/commands are present and no secret or fake URL appears.

### Step 5: Review as a new operator

Read the README and linked quickstarts in order. Check every command against `compose.yaml`, `cmd/caravan/main.go`, `cmd/caravan/prepare.go`, and the documented frontend scripts. Ensure dates/status labels make it clear which statements are normative, shipped, deferred, or manually unverified.

**Verify**: `git diff --check && git status --short` → no whitespace errors; only the four in-scope documentation files are modified.

## Test plan

- Documentation tests are command/path checks, not source tests. Use `grep` assertions for the three named contradictions and required links.
- Run `go test -count=1 ./...`, `go vet ./...`, and `(cd web && npm run check && npm test && npm run build)` only as repository-level verification after the text changes; no source should change.
- Manually follow the source-build Docker quickstart through the existing verification procedure; do not report it as passing if no Docker daemon is available.

## Done criteria

- [ ] SPEC and PLAN agree on shipped discover/requests and multi-user RBAC versus future work.
- [ ] PLAN's phase count and Phase 12 wording are internally consistent.
- [ ] Docker docs distinguish the checked-in source-build Compose path from any optional published-image path; no misleading default `pull` remains.
- [ ] Root README exists, is concise, and links to setup/verification docs and canonical commands.
- [ ] Existing manual verification caveats remain truthful.
- [ ] No source/config file changed and only Scope files are modified.

## STOP conditions

- The implementation does not actually support a claim marked shipped after checking route registration/tests.
- A contradiction requires changing product behavior or Compose configuration rather than documentation.
- No trustworthy registry/tag exists for a published-image upgrade path; do not invent one.
- A requested README section would duplicate a detailed document or require a secret, private URL, or unverified hardware claim.

## Maintenance notes

- Update shipped-status language when a roadmap interlude lands; do not silently reintroduce future labels in SPEC.
- Keep the phase count tied to headings in `docs/PLAN.md` and call interludes out separately.
- If Compose later switches from source build to a published image, update both `compose.yaml` and the Docker quickstart/upgrade paragraphs together.
- Preserve the “not yet verified end to end” evidence boundary until a dated real-host record closes it.
