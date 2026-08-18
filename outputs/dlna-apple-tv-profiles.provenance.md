# Provenance: Caravan DLNA, profile scopes, and Apple TV playback

- **Date:** 2026-08-18
- **Research rounds:** 1 focused lead-research round
- **Sources consulted:** 18 unique local files and external sources
- **Sources accepted:** 18
- **Sources rejected:** 0 broken sources; one Home Assistant issue was retained only as corroborating evidence
- **Verification:** PASS WITH NOTES. No fatal issues. The Home Assistant diagnosis and direct-URL workaround require confirmation from the user's logs and hardware. Apple's specification is not a complete AirPlay format matrix, so the target recommendations are conservative.
- **Plan:** `outputs/.plans/dlna-apple-tv-profiles.md`
- **Draft:** `outputs/.drafts/dlna-apple-tv-profiles-draft.md`
- **Citation verification:** `outputs/dlna-apple-tv-profiles-citation-verification.md`
- **Logic verification:** `outputs/dlna-apple-tv-profiles-verification.md`
- **Research files:** None. The lead performed this narrow investigation directly, followed by the required verifier and reviewer passes.
- **Code verification:** `go test ./internal/core ./internal/dlna ./internal/convert ./internal/wanted` passed.
