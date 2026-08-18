# Research Plan: Caravan DLNA, quality profiles, TV profiles, and Apple TV playback

## Questions

1. What do Caravan quality profiles and TV profiles each control?
2. Which profile, if either, affects files exposed over DLNA?
3. How does Home Assistant send a Caravan DLNA item to Apple TV, and where does compatibility fail?
4. What changes would give users reliable playback without weakening acquisition quality controls?

## Strategy

- Trace Caravan's profile models, DLNA metadata, resource URLs, MIME types, and streaming behavior in code and tests.
- Check current official Home Assistant and pyatv documentation or source for Apple TV media playback requirements.
- Compare the actual file and protocol path against Apple's supported container and codec expectations.
- Produce a short diagnosis and rank fixes by impact, complexity, and architectural fit.
- Keep the investigation focused and use no subagents.

## Acceptance Criteria

- [x] Both Caravan profile types are explained from code evidence.
- [x] The complete Caravan to Home Assistant to Apple TV playback path is identified.
- [x] The likely failure modes are separated into container, codec, transport, URL, and metadata causes.
- [x] Critical compatibility claims use official upstream documentation or source plus local code evidence.
- [x] Recommendations distinguish quick fixes from proper transcoding or remuxing support.

## Task Ledger

| ID | Owner | Task | Status | Output |
|---|---|---|---|---|
| T1 | lead | Trace Caravan profile semantics | done | `outputs/dlna-apple-tv-profiles-brief.md` |
| T2 | lead | Trace DLNA serving and metadata | done | `outputs/dlna-apple-tv-profiles-brief.md` |
| T3 | lead | Verify Home Assistant and Apple TV expectations | done | `outputs/dlna-apple-tv-profiles-citation-verification.md` |
| T4 | lead | Synthesize and prioritize improvements | done | `outputs/dlna-apple-tv-profiles-brief.md` |

## Verification Log

| Item | Method | Status | Evidence |
|---|---|---|---|
| Profile behavior | Direct code and test inspection | pass | `internal/core`, `internal/store`, `internal/wanted`, UI settings |
| DLNA response behavior | Direct code, test inspection, focused Go tests | pass | `internal/dlna`; package tests passed |
| Home Assistant routing | Pinned upstream source cross-read | pass with caveat | Home Assistant commit `1adf98231f7a41d29e739100ff4c65c047ae825f`; user log unavailable |
| Apple TV format guidance | Official Apple specification cross-read | pass with caveat | Current Apple TV 4K page is not a complete AirPlay format matrix |
| Report claims and citations | Independent verifier and reviewer | pass with notes | Citation and logic verification artifacts in `outputs/` |

## Decision Log

- This is a narrow implementation investigation, so the lead performed the research directly. The research skill's required citation verifier and logic reviewer checked the resulting brief.
- The current changelog contains no DLNA compatibility work beyond listing DLNA as an initial handoff surface.
- The primary diagnosis is labeled as an inference until the user's Home Assistant log confirms the RAOP branch.
- Apple format recommendations are conservative and require model-specific testing.
