# Citation verification: Caravan profiles and Apple TV playback

## Result

PASS WITH NOTES. The cited local files exist, the cited line ranges support the report's core claims, and every external URL returned HTTP 200 when checked. The focused test command also passed:

`go test ./internal/core ./internal/dlna ./internal/convert ./internal/wanted`

## Corrections made

- Corrected local citation paths for the final file location. Links now use `../internal/...` and `../web/...` instead of the draft's `../../...` paths.
- Removed all em dash and en dash characters from the brief to satisfy the project's writing rule.
- Narrowed the Apple format wording. Apple's current Apple TV 4K page lists AVC and HEVC playback up to 2160p and names `.m4v`, `.mp4`, and `.mov` in its H.264/AAC entry. It does not provide a complete container or codec matrix for every AirPlay scenario.
- Corrected the conversion claim so it cites the conversion worker's ffprobe call, the planner's remux and transcode decisions, and the ffmpeg arguments that add `+faststart`.
- Added citations for system, library, movie, and series quality-profile assignment and for the independent global TV-profile setting and fallback behavior.
- Added citations to the Home Assistant routing code and pyatv's `is_streamable` helper for the direct-URL workaround.
- Expanded several local line anchors so they include the exact MIME, range, profile, and conversion behavior being described.

## Sources checked

Local files checked directly:

- `internal/core/media.go`
- `internal/core/tvprofile.go`
- `internal/store/profiles.go`
- `internal/store/settings.go`
- `internal/wanted/wanted.go`
- `internal/dlna/didl.go`
- `internal/dlna/http.go`
- `internal/convert/plan.go`
- `internal/convert/convert.go`
- `internal/api/library.go`
- `web/src/lib/components/TVProfileSettings.svelte`

External URLs checked directly:

- Home Assistant DLNA DMS source at commit `1adf98231f7a41d29e739100ff4c65c047ae825f`
- Home Assistant Apple TV source at the same commit
- pyatv RAOP audio source at commit `b277a4c8222ecdcbaab8a24e3e713ca44765adb4`
- pyatv `helpers.py` on `master`
- Home Assistant DLNA DMS documentation
- Home Assistant issue 172671
- Apple's Apple TV 4K technical specifications

## Rejected or downgraded sources

No URL was rejected as broken. Home Assistant issue 172671 was retained only as corroborating evidence because it is a user-reported issue about an HTTP audio stream, not a controlled reproduction of Caravan video playback. The brief labels the overall Home Assistant diagnosis as an inference pending the user's actual Home Assistant log.

## Unresolved citation gaps

- The exact playback exception and the source file's ffprobe output were not available, so the RAOP and miniaudio explanation cannot be confirmed for this particular installation.
- The Apple specification page does not list Matroska, AV1, DTS, or TrueHD. Those conclusions are conservative absence-based recommendations, not proof that every Apple TV model rejects them in every playback path.
- Apple TV model and tvOS version were not provided. The proposed target is scoped to current Apple TV 4K specifications.
- The recommendation to persist ffprobe facts is a product design proposal. The current DTO still evaluates codec, audio, extension, and quality fields from stored media metadata, while the conversion worker probes files at conversion time.
- Source links use pinned Home Assistant and pyatv commits where available. The pyatv helper citation uses `master`, so its behavior should be rechecked if the workaround becomes an implementation contract.
