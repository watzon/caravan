# Local tracker definitions

Caravan can execute a deliberately small, fail-closed subset of Cardigann-style
YAML definitions. The engine returns normalized `core.Release` values directly
for Caravan's searches and can expose a stored local indexer as Torznab at:

```text
/api/v1/indexers/{id}/feed?t=caps
/api/v1/indexers/{id}/feed?t=search&q=...
```

When accounts exist, external Torznab clients may authenticate that feed with
Caravan's API key in the standard `apikey` query parameter. That parameter is
accepted only by this read-only feed handler; it does not authenticate the rest
of `/api/v1`.

## Compatibility boundary

Every source document is first decoded into an inert, deterministic capability
manifest (`source:id`, content digest/revision, privacy, and sorted unsupported
codes). Only a manifest with no unsupported codes that also passes the strict
compiler can enter the executable registry. The current subset is:

- public HTTP(S) trackers;
- GET and form-encoded POST search paths, including multiple fixed paths;
- typed path inputs and a small allowlist of request headers;
- Go-template path/input/header rendering with query/config values and
  `urlquery`, bounded to 64 KiB per rendered value;
- HTML rows selected with goquery-compatible CSS selectors;
- root-array or dotted-path JSON rows;
- bounded XML rows and fields addressed by dotted element paths; DTDs and
  processing instructions are rejected;
- text or attribute field extraction;
- literal field text, optional fields, and result templates evaluated only
  after source extraction;
- `tolower`, `toupper`, `trim`, `append`, `prepend`, `replace`, `re_replace`,
  `regexp`, `split`, `urldecode`, and `htmldecode` filters;
- tracker-to-Torznab category mappings;
- direct torrent/magnet links or magnet synthesis from a 40-character infohash;
- deterministic multi-path deduplication and requested-category filtering;
- structured Torznab query identifiers and extended result attributes;
- an 8 MiB response ceiling for HTML, JSON, and XML.

The production transport rejects non-public and special-use initial
destinations, redirect targets, DNS answers, URL user information, and
non-HTTP(S) schemes. It validates the address again at dial time so DNS
rebinding cannot bypass the policy. Each engine additionally restricts initial
requests and redirects to its configured base, declared links, and explicit
`*url` settings. Rendered setting URLs are omitted from returned errors.

Built-ins use the `builtin:` namespace; existing bare IDs resolve only to that
namespace. Owners may place independently obtained definitions in
`<application-data>/indexer-definitions`. Regular `.yml` files there are loaded
as `user:<id>` through a rooted, bounded directory provider. Malformed,
duplicate, symlinked, oversized, or unsupported files are quarantined without
evicting valid siblings. Executable user definitions may be configured through
the API with only their declared settings, but they are never inserted into the
advertised static catalog automatically.

## Built-in definitions

The currently embedded and cataloged definitions are The Pirate Bay (JSON API),
RuTor, Nyaa, and TokyoTosho. Each has synthetic registry/client/Torznab coverage
and a real public-site probe. A definition remains hidden unless its schema is
accepted by the strict parser and its catalog entry resolves in the registry.

Not yet supported: login/cookie flows, CAPTCHA or WAF solving, arbitrary request
bodies or headers, detail-page download resolution, row
cases/removals/defaults, pagination, persistent cookie jars, per-definition
request delays, and the wider upstream filter/template language. Such
definitions must not be added to the built-in registry until their complete
behavior is implemented and tested.

## Licensing and provenance

The interpreter is an independent Caravan implementation. Caravan does **not**
bundle the current Prowlarr/Indexers definition pack: that repository supplies
no standalone license grant, and most definitions are synchronized from
GPL-licensed Jackett. Public availability is not redistribution permission.

Every embedded definition must therefore be Caravan-authored or carry an
explicit license compatible with its distribution here, with provenance
recorded during review. The user-supplied provider loads third-party files at
runtime without presenting those files as part of Caravan's MIT-licensed source
distribution. This is engineering guidance, not legal advice.

## Immutable definition packs

`OpenSignedPackArchive` accepts an owner-supplied ZIP-based
`.caravan-indexer-pack`. It verifies an Ed25519 signature over the exact
`manifest.json` bytes using a caller-supplied trusted keyring; a key embedded in
the archive can never establish its own trust. The signed manifest carries a
format/schema version, source ID, revision, SPDX license expression, provenance,
minimum Caravan version, license/notice digests, exact payload totals, and each
definition's metadata ID, SHA-256 digest, and approved HTTP(S) origins.

Validation rejects unknown signers, bad signatures, reserved sources, unknown
JSON fields, extra/missing archive members, non-regular entries, traversal,
backslashes, non-ASCII or duplicate case-folded paths, digest/identity/origin
mismatches, unsupported versions, and fixed archive/file/count/aggregate limits.
It supports up to 4,096 definitions, separately from the 256-file loose
`DirectoryProvider` limit.

A validated `PackCandidate` is inert and deliberately does not implement
`Provider`: it cannot enter the executable registry before a later activation
tranche adds durable revision/digest pins, license acceptance, atomic snapshots,
and last-known-good rollback. Unsupported definitions remain display-only.
`DescribeProvider` reports `metadata-only`, `source-not-installed`,
`unsupported`, `quarantined`, and `runnable-unverified` lifecycle states for
callers that need diagnostics. `ClassifyCorpusDirectory` is a read-only,
deterministic current-compiler audit helper for a direct external YAML corpus;
it reports exact total/runnable/inert counts and capability histogram without
copying corpus bytes into Caravan or registering them for execution.
