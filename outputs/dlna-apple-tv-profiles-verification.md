# Verification review: Caravan profiles and Apple TV playback

## Overall result

PASS WITH NOTES. No FATAL issue was found. The distinction between quality profiles and TV profiles is grounded in Caravan's code, and the Home Assistant media-source route is accurately traced for the cited Home Assistant and pyatv revisions. The focused Caravan test command also passes:

`go test ./internal/core ./internal/dlna ./internal/convert ./internal/wanted`

The two findings below should be addressed before presenting the workaround and Apple TV target as verified compatibility guarantees.

## Findings

### MAJOR-1: The direct-URL workaround is conditional, not experimentally verified

The workaround follows from the cited Home Assistant code, but the brief presents it as a practical workaround without showing a real request or a Home Assistant regression test.

The code path is sound as an inference:

1. A `media-source://dlna_dms/...` request is resolved by Home Assistant to a URL and MIME type in [`dms.py`](https://github.com/home-assistant/core/blob/1adf98231f7a41d29e739100ff4c65c047ae825f/homeassistant/components/dlna_dms/dms.py#L530-L544).
2. The Apple TV integration then unconditionally changes the media type to `music` before choosing the RAOP branch in [`media_player.py`](https://github.com/home-assistant/core/blob/1adf98231f7a41d29e739100ff4c65c047ae825f/homeassistant/components/apple_tv/media_player.py#L365-L387).
3. Supplying a direct URL with media type `video` bypasses that media-source block. The RAOP branch is then selected only if `is_streamable(media_id)` returns true.
4. The cited pyatv helper calls `miniaudio.get_file_info(filename)` and returns false on an exception, so a normal remote video URL is expected to fall through to `play_url`.

The last step is still an inference. The helper is documented and typed as a filename check, not as a tested remote-URL classifier, and the brief has no captured Home Assistant log or service-call reproduction. The report should call this an expected workaround and include an exact YAML or service-call example plus the expected `Playing ... via AirPlay` log line. If `is_streamable` changes or miniaudio accepts the URL, the workaround could still select RAOP.

### MAJOR-2: The Apple format table overstates what the Apple page directly establishes

The Apple TV 4K specification supports separate claims for AVC/HEVC video and AAC, AC-3, and E-AC-3 audio. It explicitly names `.m4v`, `.mp4`, and `.mov` in the H.264 Baseline plus AAC entry. It does not provide the complete container, codec-profile, audio-track, and AirPlay pairing matrix used by the brief's table.

In particular, the statement that "MP4 with HEVC and AAC, AC3, or EAC3" is supported is a reasonable current-device hypothesis, but it is not directly established by the cited page as a combined playback profile. Likewise, the absence of MKV, AV1, DTS, and TrueHD is useful for a conservative preset, but absence from this page is not proof that every Apple TV model or AirPlay path rejects each format.

The brief already narrows the proposed target to current Apple TV 4K hardware and notes the evidence gap in Open questions. To resolve the finding, label the table's pairing and rejection statements as conservative inferences, or cite an Apple media-format source that explicitly covers AirPlay URL playback. Keep model and tvOS version as required test dimensions.

## Minor findings

### MINOR-1: Several summary citations are too narrow for the claims they carry

The citation to [`internal/core/media.go#L265`](../internal/core/media.go#L265) points to TV compatibility policy constants, while the quality-profile fields that establish the acquisition role are at [`internal/core/media.go#L281-L311`](../internal/core/media.go#L281-L311). The adjacent [`tvprofile.go#L81`](../internal/core/tvprofile.go#L81) citation establishes the TV capability model, but not the global warning and conversion consumers by itself. The report later cites more relevant code, so this is a traceability issue rather than a conclusion failure.

Similarly, [`TVProfileSettings.svelte#L4-L9`](../web/src/lib/components/TVProfileSettings.svelte#L4-L9) is a source comment. The rendered setting description and save behavior are at [`TVProfileSettings.svelte#L70-L116`](../web/src/lib/components/TVProfileSettings.svelte#L70-L116). Updating the summary anchors would make the evidence easier to audit.

### MINOR-2: The "capable is not an Apple TV profile" conclusion is appropriately conservative, but should remain labeled as such

Caravan's local code clearly shows that `capable` accepts MKV and AV1, while the Apple page does not make a general AV1 or Matroska promise. That supports avoiding `capable` as an Apple preset. It does not establish universal rejection, and the brief's current Open questions language correctly treats this as an absence-based recommendation. Preserve that wording if the table is revised.

### MINOR-3: The pyatv helper citation is unpinned

The workaround cites `pyatv/helpers.py` on `master`, while the RAOP source is pinned to a commit. The brief already calls this out. Pin the helper to the same tested revision, or add a recheck requirement before making the workaround an implementation contract.

## Logical consistency check

- The quality-profile versus global TV-profile distinction is internally consistent. Quality-profile TV settings affect acquisition scoring through `ignore`, `prefer`, and `require`; the global setting feeds release/file compatibility views and conversion.
- The claim that Caravan's DLNA endpoint serves the original file without transcoding is supported by the handler and DIDL flags.
- The claim that the existing conversion path can remux a compatible stream set or transcode incompatible streams is supported by the planner, ffmpeg arguments, and worker probe flow.
- The report correctly separates the Home Assistant routing problem from native Apple TV format compatibility. Fixing the routing branch alone does not make MKV, AV1, or unsupported audio playable.
- No contradiction was found between the recommendations and the stated open questions, provided the direct URL and Apple format findings are treated as conditional and conservative.

## Single-source and confidence check

The central Home Assistant diagnosis relies on one Home Assistant revision plus pyatv source, but it is a multi-source code trace rather than a single-source claim. The report explicitly says it remains an inference pending the user's log. The Apple compatibility recommendations rely mainly on one official Apple specification page and should retain the current-device and model-specific testing caveat. No FATAL confidence mismatch was found.

## Required follow-up

1. Reproduce the failed media-source call with Home Assistant debug logging and record whether the `Streaming ... via RAOP` path and miniaudio exception occur.
2. Test the direct URL call with `media_content_type: video`, a converted MP4, and an Apple TV model and tvOS version recorded.
3. Downgrade the Apple format table's combined codec and audio claims to conservative inferences unless a source with explicit AirPlay playback combinations is added.
4. Add the missing provenance sidecar required by the deep-research workflow if it is not created elsewhere.
