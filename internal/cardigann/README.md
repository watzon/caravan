# Local tracker definitions

Caravan runs Cardigann-style YAML tracker definitions itself. The engine
returns normalized `core.Release` values for Caravan's searches and exposes any
stored local indexer as a Torznab feed at:

```text
/api/v1/indexers/{id}/feed?t=caps
/api/v1/indexers/{id}/feed?t=search&q=...
```

External Torznab clients authenticate that feed with Caravan's API key in the
standard `apikey` query parameter. The parameter is accepted only by this
read-only feed handler.

## Definition sources

- **Managed.** On every start, `serve` refreshes a snapshot of the Prowlarr
  indexer definition list (`indexers.prowlarr.com`) into
  `<application-data>/managed-definitions`, verifies each file against the
  listing's Git blob SHA, and loads it. Definitions are addressed as
  `managed:<id>`. An install that cannot reach the network keeps the last
  verified snapshot.
- **Built-in.** A few Caravan-authored definitions ship in `definitions/` as
  `builtin:<id>` and cover the synthetic test suites.
- **User.** Regular `.yml` files in `<application-data>/indexer-definitions`
  load as `user:<id>` through a rooted, bounded directory provider. Malformed,
  duplicate, symlinked, or oversized files are quarantined without evicting
  valid siblings.
- **Packs.** `OpenSignedPackArchive` validates an Ed25519-signed
  `.caravan-indexer-pack` against a trusted keyring. Installed packs are pinned
  by source, revision, and digest.

Every source document is first decoded into an inert capability manifest
(`source:id`, digest, privacy, sorted unsupported codes). Only a manifest with
no unsupported codes that also passes the strict compiler enters the
executable registry. The catalog API reports each site's state so the UI can
show what is addable and why the rest is not.

## What runs

- public and private HTTP(S) trackers;
- GET and form-encoded POST search paths, multiple paths, typed inputs, and an
  allowlist of request headers;
- login by cookie, GET, POST, or form (the default), including selector
  inputs, submit paths, login tests, and error selectors;
- a **session cookie** setting that Caravan adds to every login definition. A
  pasted browser session replaces the login flow, which is the only way past
  trackers that show a captcha on the login form. When the declared captcha
  appears, the error says so and points at that setting;
- **browser challenges.** Definitions that declare `info_flaresolverr`, or any
  site that answers with a Cloudflare or DDoS-Guard interstitial, are routed
  through the FlareSolverr endpoint saved in Settings > Indexers. The solver's
  cookies and user agent are reused for the rest of the session, including
  torrent downloads. Without an endpoint the search fails with an error that
  names the setting;
- Go-template rendering of paths, inputs, and headers with query and config
  values, bounded to 64 KiB per rendered value; a stray closing parenthesis in
  an upstream template is dropped rather than failing the site;
- HTML rows by CSS selector, JSON rows by root array or dotted path, bounded
  XML rows; text or attribute extraction; the upstream filter set (`re_replace`,
  `dateparse`, `split`, `trim`, and the rest);
- category mappings, direct torrent or magnet links, magnet synthesis from an
  infohash, detail-page download resolution, deduplication, and requested-
  category filtering;
- an 8 MiB response ceiling for HTML, JSON, and XML.

The production transport rejects non-public and special-use destinations,
redirect targets, DNS answers, URL user information, and non-HTTP(S) schemes,
and validates the address again at dial time. Each engine also restricts
requests and redirects to its configured base, declared links, and explicit
URL settings. FlareSolverr is the one exception: it is owner-configured and
usually lives on the LAN, so it uses a separate unrestricted client.

## What does not run

- `certificates`: definitions that pin a self-signed TLS fingerprint (about a
  dozen private trackers).
- Definitions whose YAML the strict parser rejects (a handful of upstream files
  with invalid escape sequences).
- `script`, filesystem or environment interpolation, and login methods other
  than the four above.
- Solving a login captcha. Use the session cookie setting instead.

Such definitions stay visible in the catalog with their reason and are never
inserted into the executable registry.

## Licensing and provenance

The interpreter is an independent Caravan implementation. Caravan does not
bundle the Prowlarr definition pack in its source tree: the managed snapshot
is downloaded at runtime into the owner's application data, and the built-in
definitions are Caravan-authored. This is engineering guidance, not legal
advice.
