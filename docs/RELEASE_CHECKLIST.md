# Initial release checklist

Target: `v0.1.0` — an early public release, not a production-readiness claim.

## Blocking the release

- [x] Choose and add a top-level `LICENSE`. MIT was selected and added.
- [x] Create the `watzon/caravan` GitHub repository and add it as `origin`.
- [ ] Push `main`, enable Actions, and confirm every job in `CI` passes remotely.
- [ ] Confirm a release tag outside `main` is rejected and archive builds wait
      for the release workflow's frontend, Go, race, and vulnerability gates.
- [ ] Enable GitHub private vulnerability reporting so `SECURITY.md` has a live
      contact path.
- [ ] Review bundled dependency licenses and any attribution/NOTICE duties,
      including embedded font files and binary release distribution.
- [ ] Run a release candidate through the tag workflow without announcing it;
      verify all five archives, the checksum manifest, and embedded version.
- [x] Confirm `.env`, `config/`, and `data/` are excluded from Git and Docker
      build contexts.

## Strongly recommended before announcing

- [ ] Record the real exFAT portable-drive matrix in `docs/portable.md`: macOS,
      Windows, a second architecture where available, safe/dirty eject, and TV
      USB playback.
- [ ] Complete the real qBittorrent and SABnzbd verification procedure in
      `docs/external-clients.md`; test NZBGet too if it is advertised equally,
      including one completed import through a remote path mapping.
- [ ] Complete a real-host hardlink import and ownership check from
      `docs/docker.md`.
- [x] Accept the trusted-LAN first-boot model. Compose still publishes port
      8677 on the LAN; first-run documentation requires immediate administrator
      setup, with loopback or reverse-proxy publishing for untrusted networks.
- [x] Choose whether Docker is checkout-only for `v0.1.0` or publish a versioned
      registry image; make the README and announcement explicit either way.
- [ ] Capture two sanitized screenshots: first-run/discover and queue/library.
- [x] Replace `TBD` in `CHANGELOG.md` with the release date.
- [ ] Review `docs/SPEC.md` and `docs/PLAN.md` for internal/draft language; keep
      them as design history or move unfinished promises to issues.
- [ ] Confirm the repository description, topics, issue tracker, and support
      expectations on GitHub.

## Release-day verification

- [ ] Start from a clean checkout.
- [x] Run `(cd web && npm ci && npm run check && npm test && npm run build)`.
- [x] Run `go test -count=1 ./...`, `go vet ./...`, and `go build ./...`.
- [x] Run `govulncheck ./...`, full `npm audit`, and `npm audit --omit=dev`.
- [x] Build the Docker image and run `caravan version` inside it.
- [ ] Ensure `git status --short` contains only reviewed release-prep changes and
      the tag points at reviewed `main`.
- [ ] Push `v0.1.0`; inspect the generated GitHub Release before sharing it.
- [ ] Download one published archive on a clean host, verify the SHA-256
      manifest, run `caravan version`, and complete first-run setup.
- [ ] Publish the announcement only after the clean-host smoke test passes.

## Explicitly after the first release

- [ ] Add code signing/notarization for Windows and macOS binaries.
- [ ] Add SBOMs and artifact provenance/signing.
- [ ] Publish and maintain a versioned container image if demand justifies it.
- [ ] Add a compatibility matrix based on real user reports rather than claims.
