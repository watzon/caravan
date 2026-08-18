# Caravan profiles and Apple TV playback through Home Assistant

## Executive summary

The two profile types answer different questions:

| Setting | Question it answers | Effect |
| --- | --- | --- |
| Quality profile | "Which release should Caravan acquire, and when should it upgrade?" | Filters and scores candidates by resolution, source, size, seeders, custom formats, and optional TV compatibility. |
| Global TV profile | "What can the main playback target decode?" | Produces compatibility warnings and controls the convert-for-TV output target. |
| TV target inside a quality profile | "Should this acquisition policy prefer or require compatibility with a target?" | Changes release scoring only for items using that quality profile. |

The third row is why the UI feels like it has overlapping settings. Caravan uses the same built-in TV capability presets in two separate scopes. The global selection controls warnings and conversion, while each quality profile can independently select a TV target for acquisition scoring. These selections can disagree. The model and comments in [internal/core/media.go](../../internal/core/media.go#L265) and [internal/core/tvprofile.go](../../internal/core/tvprofile.go#L81) confirm those roles.

For the exact playback route described, the most likely immediate failure is in Home Assistant, before Apple TV gets a useful video playback request. Home Assistant correctly resolves Caravan's DLNA item to an HTTP URL and MIME type. Its Apple TV integration then overwrites the resolved media type with `music` and chooses pyatv's RAOP `stream_file` path. That path is implemented as an audio decoder and PCM audio source. A video selected from any DLNA server can therefore be sent down an audio-only route. This is independent of Caravan's quality or TV profile selection. [Home Assistant DLNA resolver](https://github.com/home-assistant/core/blob/1adf98231f7a41d29e739100ff4c65c047ae825f/homeassistant/components/dlna_dms/dms.py#L530-L544), [Home Assistant Apple TV playback](https://github.com/home-assistant/core/blob/1adf98231f7a41d29e739100ff4c65c047ae825f/homeassistant/components/apple_tv/media_player.py#L352-L387), [pyatv RAOP audio source](https://github.com/postlund/pyatv/blob/b277a4c8222ecdcbaab8a24e3e713ca44765adb4/pyatv/protocols/raop/audio_source.py#L552-L620).

There is also a real second layer of compatibility. Caravan's DLNA server sends the original file without remuxing or transcoding. Apple lists MP4, M4V, and MOV for progressive video playback, with AVC or HEVC support depending on the stream. Matroska is not listed. Caravan's `capable` preset accepts MKV and AV1, so it is not an Apple TV profile. The `safe` preset is closer and the existing conversion system can create H.264/AAC MP4 with fast-start metadata, but Home Assistant still needs to route that video through regular AirPlay rather than RAOP audio. [Caravan DLNA serving](../../internal/dlna/http.go#L341), [Caravan conversion planning](../../internal/convert/plan.go#L69), [Apple TV 4K specifications](https://www.apple.com/apple-tv-4k/specs/).

## What each Caravan profile does

### Quality profiles are acquisition policies

A quality profile is assigned at the system, library, movie, or series level. It decides:

- acceptable resolutions and the upgrade cutoff;
- preferred release sources;
- minimum seeders and size bounds;
- proper and repack scoring;
- custom-format scores;
- whether a selected TV target is ignored, preferred, or required.

The TV compatibility policy is applied by the release scorer. `ignore` makes compatibility irrelevant, `prefer` adjusts the score, and `require` rejects a release only when known tags conflict with the selected TV target. Unknown information is not rejected. [Release scoring implementation](../../internal/wanted/wanted.go#L69).

A quality profile does not transform an existing file and does not change a DLNA response. Its compatibility judgment is also limited at acquisition time because it mostly relies on tags parsed from the release name.

### The global TV profile is a warning and conversion target

The Playback setting selects one built-in target capability description. Caravan uses it to:

- show `compatible`, `needs-remux`, `incompatible`, or `unknown` on releases and imported files;
- populate the Convert page;
- choose the target container, video codec, audio codec, bit depth, and maximum resolution when conversion is requested.

It does not automatically change what Caravan grabs. It does not make DLNA transcode on demand. This is explicit in both the Playback UI and the DLNA implementation. [Playback settings component](../../web/src/lib/components/TVProfileSettings.svelte#L2), [DLNA handler](../../internal/dlna/http.go#L341).

### There are two independent TV target selections

The global Playback selection and the target stored inside a quality profile are separate values:

- global `tv_profile` setting: warnings and conversion;
- `quality_profiles.tv_profile`: release scoring for that quality profile.

Both fall back to `safe`, but changing the global Playback selection does not update quality profiles. This duplication is the main conceptual problem. A user can see release warnings against one target while Caravan scores the same release against another.

## The failing playback path

The route is:

1. Caravan publishes an item with a URL such as `/dlna/media/123.mkv`, a MIME type derived from the extension, and byte-range support. It declares that the content is not transcoded. [DIDL metadata](../../internal/dlna/didl.go#L30), [HTTP media response](../../internal/dlna/http.go#L384).
2. Home Assistant's DLNA DMS integration browses that item and resolves the chosen resource to its direct HTTP URL and MIME type. Home Assistant documents configured DLNA servers as media sources. [DLNA DMS documentation](https://www.home-assistant.io/integrations/dlna_dms/), [resolver source](https://github.com/home-assistant/core/blob/1adf98231f7a41d29e739100ff4c65c047ae825f/homeassistant/components/dlna_dms/dms.py#L530-L544).
3. The Apple TV integration detects the media-source URI, resolves it, then unconditionally assigns `MediaType.MUSIC`.
4. Because the item is now labeled music and modern Apple TV connections expose `StreamFile`, Home Assistant calls `stream_file` through RAOP.
5. pyatv's RAOP implementation creates an `AudioSource`, downloads the HTTP body, and passes it to miniaudio for audio decoding. It does not present the video URL to Apple TV's video player.

This source-level explanation matches the symptom and is a high-confidence diagnosis, but it remains an inference until the failing Home Assistant log is checked. A recent Home Assistant issue reports the same RAOP and miniaudio path failing even for a valid HTTP audio stream, which further shows that this branch is the one producing current streaming errors. [Home Assistant issue 172671](https://github.com/home-assistant/core/issues/172671).

## Compatibility after the Home Assistant routing bug

Once Home Assistant uses regular AirPlay `play_url` for video, the file still has to be natively playable:

| Caravan output | Apple TV expectation | Result |
| --- | --- | --- |
| MKV with H.264 or HEVC | MKV is not listed in Apple's supported progressive file formats | Usually needs a copy-only remux to MP4. |
| MP4 with H.264 and AAC | Listed by Apple and matches Caravan's safe target | Best compatibility choice. |
| MP4 with HEVC and AAC, AC3, or EAC3 | Supported by current Apple TV 4K specifications, subject to stream profile details | Good modern target. |
| AV1 | Caravan's capable target accepts it, but Apple's current Apple TV 4K specifications do not make a general AV1 playback promise | Do not include it in an Apple TV preset without model-specific testing. |
| DTS or TrueHD audio | Not listed in Apple's ordinary audio formats for this path | Convert audio to AAC, AC3, or EAC3. |

Caravan already has the machinery for the first and last cases. Its conversion planner probes the actual file, performs a stream-copy remux when only the container is wrong, and re-encodes only incompatible streams when necessary. Its MP4 output uses fast-start metadata. [Conversion planner](../../internal/convert/plan.go#L69), [ffmpeg arguments](../../internal/convert/plan.go#L275).

One remaining accuracy problem is that library compatibility badges use codec and audio fields originally derived from filenames, plus the file extension. The conversion worker uses ffprobe and therefore has better information. The probe result should be persisted and reused by badges and DLNA resource selection. [Library compatibility DTO](../../internal/api/library.go#L188), [ffprobe model](../../internal/convert/plan.go#L13).

## Recommended changes

### 1. Fix or work around Home Assistant's video routing

This is the blocking fix for the reported route. Submit an upstream Home Assistant issue or PR that:

- preserves the resolved `play_item.mime_type`;
- uses RAOP `stream_file` only for `audio/*` resources;
- uses AirPlay `play_url` for `video/*` HTTP resources;
- adds a regression test where a `media-source://dlna_dms/...` item resolves to `video/mp4` and asserts that `play_url`, not `stream_file`, is called.

Caravan cannot force this choice from DLNA metadata because Home Assistant already receives the correct `video/*` MIME type and then overwrites the media type.

A practical workaround is to convert the file with Caravan's `safe` target, then call Home Assistant's `media_player.play_media` with the direct Caravan HTTP URL and `media_content_type: video`. Do not use `url` for the content type because the current Apple TV integration treats `url` as an app identifier. This avoids the media-source branch and lets Home Assistant fall through to `play_url`. The Apple TV must be able to reach Caravan's LAN URL directly.

### 2. Add an Apple TV or AirPlay playback target

Add a built-in target with conservative current Apple TV 4K capabilities:

- containers: MP4, M4V, MOV;
- video: H.264 and HEVC;
- bit depth: up to 10-bit;
- audio: AAC, AC3, EAC3;
- resolution: up to 2160p;
- no MKV, AV1, DTS, or TrueHD by default.

The existing `safe` target remains the broad fallback. The current `capable` target should not be described as suitable for Apple TV because it accepts MKV and AV1.

### 3. Remove the hidden profile split

Rename and scope the settings:

- global `TV profile` becomes `Playback target`, with the subtitle `Used for file warnings and conversion`;
- the quality-profile field becomes `Acquisition compatibility target`, with an `Use active playback target` option;
- show a warning when a quality profile explicitly targets something different from the active playback target.

New quality profiles should inherit the active playback target instead of silently pinning `safe`. Existing explicit selections can remain unchanged during migration.

### 4. Probe files once and reuse the result

Run ffprobe during import or the first compatibility check, persist the normalized stream facts, and reuse them for:

- compatibility badges;
- conversion planning;
- DLNA metadata and resource ordering;
- diagnostics explaining why a player may reject a file.

This avoids claiming compatibility based on a release filename that omitted bit depth, audio, or codec details.

### 5. Make direct playback diagnosable

Add these small product improvements:

- a `Copy stream URL` action on a media file;
- a playback diagnostics panel showing container, codecs, MIME type, range support, active target, and actual ffprobe verdict;
- DLNA copy that clearly says original files are served without transcoding and links incompatible files to Convert;
- a Home Assistant note with the direct-URL workaround until the upstream routing issue is fixed.

## Suggested order of work

1. File the Home Assistant issue or PR and add the direct-URL workaround to Caravan's DLNA documentation.
2. Add the Apple TV playback target and clarify the two profile scopes in the UI.
3. Make quality profiles inherit the active playback target unless explicitly overridden.
4. Persist ffprobe metadata and drive badges from real stream data.
5. Consider HLS or per-client streaming only if prepared-file playback still fails after these changes. The current convert-once design is simpler and already fits Apple TV well.

## Verification

- Reviewed Caravan's profile, scoring, conversion, DLNA DIDL, and media-serving code.
- Ran `go test ./internal/core ./internal/dlna ./internal/convert ./internal/wanted`; all packages passed.
- Cross-read current Home Assistant DLNA DMS and Apple TV integration source at commit `1adf98231f7a41d29e739100ff4c65c047ae825f`.
- Cross-read current pyatv RAOP source at commit `b277a4c8222ecdcbaab8a24e3e713ca44765adb4`.
- Checked Apple's current Apple TV 4K format specifications.

## Open questions

- The exact Home Assistant exception and the source file's ffprobe output were not available. Those would distinguish the predicted RAOP/miniaudio failure from a secondary network or native-format failure.
- Apple TV model and tvOS version were not specified. The proposed Apple TV target is conservative for current Apple TV 4K hardware, but older models may need a narrower target.
- Subtitle compatibility is not modeled. Caravan's conversions currently drop subtitles when writing MP4, which may be acceptable for compatibility but should be explicit in the UI.
