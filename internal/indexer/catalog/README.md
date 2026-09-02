# Indexer catalog

`sites.json` is retained research metadata: public facts (id, name,
description, privacy, language, public origins, content types) compiled from
the Prowlarr/Indexers definition list. The catalog API exposes all 542 rows in
its separate `inventory` field with lifecycle and addability state. They never
enter `catalog.All()` or the operational `definitions` array merely because a
homepage is known. Inventory origins are named `metadata_urls`, not feed URLs.

Content tags are derived from advertised category labels (Movies, TV,
TV/Anime, Audio, Books, XXX, PC).

Definitions that `internal/cardigann` can run are marked with
`definition_id`; their site URL is a scraper base and is never treated as a
Torznab endpoint. Direct Torznab/Newznab presets keep an empty `definition_id`
and go through the remote feed client. At runtime the managed Prowlarr
snapshot supplies the executable definition for most inventory rows.

Executable local presets, Usenet presets, and generic
Jackett/Prowlarr/Newznab/Torznab rows live in `presets.go`. The executable YAML
definitions themselves live in `internal/cardigann/definitions`.

`check_mirrors.py` probes every catalog URL in parallel and prints the ones
that do not answer. It never edits `sites.json`. Dead and leftover mirrors
are pruned by hand from that report. A site with no remaining live URL
stays in the directory so a replacement can be found.
