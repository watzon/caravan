# Contributing to Caravan

Caravan is early software. Bug reports, reproducible compatibility findings,
documentation corrections, and focused pull requests are welcome.

## Before opening a change

1. Search existing issues and pull requests.
2. For behavior changes, open an issue first so the product and compatibility
   contract can be agreed before implementation.
3. Never include credentials, private indexer URLs, copyrighted media, or real
   account data in an issue, fixture, log, screenshot, or commit.

## Development setup

Source builds require Go 1.26.5, Node.js 22, and npm. Build the embedded web
application before running Go commands on a fresh checkout:

```sh
cd web
npm ci
npm run check
npm test
npm run build
cd ..
go test -count=1 ./...
go vet ./...
go build ./...
```

Docker changes should also pass:

```sh
docker build --build-arg VERSION=contributor -t caravan:contributor .
docker run --rm caravan:contributor version
```

## Pull requests

- Keep changes scoped and explain the user-visible behavior.
- Add a regression test before fixing a bug where practical.
- Add release-parser failures to the versioned parser corpus.
- Keep database paths relative to the configured storage root.
- Preserve the privacy boundary of restricted libraries across API, UI, DLNA,
  prepared drives, notifications, logs, and shared views.
- Update user-facing documentation and `CHANGELOG.md` when behavior changes.

By contributing, you agree that your contribution is provided under the
[MIT License](LICENSE).
