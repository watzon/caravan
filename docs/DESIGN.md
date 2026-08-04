# Caravan Design System

**Status:** v1.0
**Date:** 2026-07-31
**Companion:** `SPEC.md` §5.1 (modules → screens), `PLAN.md` phase 1 task 9 (Svelte SPA)

The web UI is a Svelte SPA styled with Tailwind CSS v4 (CSS-first config, no `tailwind.config.ts`). Everything below is the single source of truth for tokens; the `@theme` block ships verbatim as `web/src/app.css` in phase 1. Paper mockups live in the "Caravan — Web UI" Paper file: https://app.paper.design/file/01KYX5WSRFDXZZ0J4JVZ4115JS. Coverage: First Run · Library (Movies, Series) · Add Movie (TMDB modal) · Scan Review (unmatched) · Movie Detail · Series Detail · Interactive Release Picker · Wanted · Queue · History · Convert-for-TV · Settings (Indexers, Quality Profiles, Storage) · portable-integrity states (safe shutdown, dirty-eject recovery) · empty/auth states (empty library, no indexers, login, password nag, loading skeletons) · phases 8–11: Settings — Libraries (per-library indexers/categories, routing, reach) · Settings — Adult Content (master switch, stash-box source, member grants, exposure summary) · Settings — Playback variant (nested adult DLNA toggle, Stash card) · Library — Adult (site grid) · Site — Detail (years as seasons, scene rows) · First Run — with Metadata step (TMDB key + inline test) · Settings — Adult Content (Enable modal: stash-box credentials gate) · phase 12: Explore — Movies (filter browse: scope row, filter rail, applied chips, cast typeahead popover) · Explore — Adult (scene browse: site/performer/tag filters, any-all tag mode, 16:9 duration-badged scene cards).

---

## 1. Direction

**Mood: "rusted" — graphite × rust.** Caravan's chrome is a quiet, warm-graphite instrument panel; the posters are the color. One intense accent — rust — marks exactly the places a user should look or act: primary buttons, active nav, progress, the wanted state. Data surfaces (release names, file paths, codec badges) render in monospace because scene names *are* data and users pattern-match them character by character.

Deliberately avoided: Plex amber, Jellyfin purple, Sonarr blue, Radarr gold, Overseerr indigo. Caravan should be recognizable at a glance in a tab strip full of its neighbors.

**Dark-first.** Media libraries are browsed in living rooms and at night; posters pop on dark chrome. The token architecture is semantic, so a light theme is a later value-swap, not a redesign — but dark is the design target and the default.

## 2. Principles

1. **Posters are the interface.** Chrome recedes: no cards-within-cards, information sits directly on surfaces, borders over shadows.
2. **One accent, spent carefully.** Rust means "act here" or "in progress." If a screen has more than three rust elements, something is misprioritized.
3. **Status is color-coded, consistently, everywhere.** Moss = present/healthy, rust = wanted/active, amber = below cutoff/warning, red = failed/missing, dusk blue = informational/seeding. The same mapping on badges, dots, progress bars, and table rows.
4. **Monospace for machine text.** Release names, paths, hashes, codec/quality badges. Never for prose or labels.
5. **Density is a feature.** This is a productivity tool for people who run servers. Comfortable 14px base, tight tables, no oversized marketing spacing.

## 3. Tokens (`app.css`, Tailwind v4)

```css
@import "tailwindcss";

@theme {
  /* ---- Fonts (self-hosted via @font-face, bundled in go:embed — no CDN) ---- */
  --font-display: "Space Grotesk", ui-sans-serif, sans-serif;
  --font-sans: "Inter", ui-sans-serif, system-ui, sans-serif;
  --font-mono: "JetBrains Mono", ui-monospace, monospace;

  /* ---- Neutrals: warm graphite ground, bone ink ---- */
  --color-bg: #171614;            /* app background */
  --color-surface: #1F1D1A;       /* sidebar, panels, table headers */
  --color-raised: #26231F;        /* cards, inputs, hover rows */
  --color-overlay: #2E2A25;       /* dropdowns, modals */
  --color-border: #35312B;        /* default hairline */
  --color-border-strong: #4A443C; /* inputs, emphasized dividers */

  --color-ink: #EDE9E3;           /* primary text */
  --color-ink-secondary: #A39C91; /* labels, metadata */
  --color-ink-muted: #756E63;     /* placeholders, disabled */
  --color-ink-inverse: #171614;   /* text on accent */

  /* ---- Accent: rust ---- */
  --color-accent: #D9622B;        /* primary actions, active nav, progress */
  --color-accent-hover: #E47440;  /* hover/active state of accent fills */
  --color-accent-tint: #3A2317;   /* selected row wash, active nav bg */
  --color-accent-text: #E8814F;   /* rust used AS text on dark (higher L) */

  /* ---- Semantics (same desert scene) ---- */
  --color-success: #8AA764;       /* moss — imported, healthy, seeding done */
  --color-success-tint: #232A1B;
  --color-warning: #E0A83C;       /* sun amber — below cutoff, degraded */
  --color-warning-tint: #332A15;
  --color-danger: #D64545;        /* signal red — failed, missing, dirty eject */
  --color-danger-tint: #331B1B;
  --color-info: #6B93C4;          /* dusk blue — seeding, informational */
  --color-info-tint: #1C2531;

  /* ---- Type scale ---- */
  --text-xs: 12px;    --leading-xs: 16px;   /* badges, table meta — use sparingly */
  --text-sm: 13px;    --leading-sm: 20px;   /* tables, secondary UI */
  --text-base: 14px;  --leading-base: 20px; /* default UI */
  --text-md: 16px;    --leading-md: 24px;   /* emphasized body, input text */
  --text-lg: 18px;    --leading-lg: 28px;   /* section titles */
  --text-xl: 24px;    --leading-xl: 32px;   /* page titles */
  --text-2xl: 32px;   --leading-2xl: 40px;  /* item detail titles */

  --font-weight-regular: 400;
  --font-weight-medium: 500;
  --font-weight-semibold: 600;
  --font-weight-bold: 700;

  --tracking-tight: -0.02em;  /* display type ≥24px */
  --tracking-normal: 0em;
  --tracking-wide: 0.08em;    /* all-caps micro labels */

  /* ---- Space / radius ---- */
  --spacing-1: 4px;  --spacing-2: 8px;  --spacing-3: 12px; --spacing-4: 16px;
  --spacing-6: 24px; --spacing-8: 32px; --spacing-12: 48px; --spacing-16: 64px;

  --radius-sm: 4px;   /* badges, inputs */
  --radius-md: 8px;   /* buttons, cards, posters */
  --radius-lg: 12px;  /* modals, panels */
  --radius-full: 9999px;

  /* ---- Breakpoints / containers ---- */
  --breakpoint-sm: 640px; --breakpoint-md: 768px;
  --breakpoint-lg: 1024px; --breakpoint-xl: 1280px;
  --container-content: 1120px; --container-wide: 1360px;
}

@layer base {
  body { @apply bg-bg text-ink font-sans antialiased; }
  * { @apply border-border; }
}
```

### Usage rules

- `--color-accent` for fills (buttons, progress, active-nav bar). For rust-colored *text or icons* on dark surfaces use `--color-accent-text` (`#E8814F`) — the base rust fails contrast at small sizes.
- Text on `--color-accent` fills is `--color-ink-inverse` (dark), not white.
- 12px text is allowed only in all-caps micro labels (`tracking-wide`, `--color-ink-secondary`) and inside badges.
- Never introduce a new hex in a component. New need → new token, added here first.

## 4. Type system

| Role | Font | Size/weight | Example |
|---|---|---|---|
| Brand wordmark | Space Grotesk 700, tight | 18px | CARAVAN in sidebar |
| Page title | Space Grotesk 600, tight | 24px | "Library" |
| Item title | Space Grotesk 600, tight | 32px | "Big Buck Bunny" on detail |
| Section title | Inter 600 | 16–18px | "Season 01" |
| Body / controls | Inter 400–500 | 14px | nearly everything |
| Table text | Inter 400 | 13px | lists, queues |
| Micro label | Inter 600, caps, wide | 12px | "QUALITY PROFILE" |
| Machine text | JetBrains Mono 400–500 | 12–13px | release names, paths, badges |

## 5. Layout

**App shell:** fixed left sidebar (240px, `--color-surface`, hairline right border) + full-bleed content on `--color-bg`. Top bar inside the content column: page title left, global search (⌘K) and system health right.

**Sidebar nav** (phase-gated; items appear as phases ship):
Library (Movies, Series) · Wanted · Calendar · Activity (Queue, History) · Convert · Settings. Active item: rust text + `--color-accent-tint` background + 2px rust left bar. The calendar is one combined view — movies and episodes together, chips colored by the standard status vocabulary, with a small type glyph (film/TV) distinguishing them. A persistent bottom slot holds system status (disk free, engine health) and — in portable mode — the **Shut down safely** button, always visible, never buried.

**Poster grid:** responsive columns (2:3 posters, `--radius-md`, 16px gap), title + year below in 13px, status dot top-left of poster (moss/amber/red/rust ring per §2.3). Hover raises a hairline `--color-border-strong` ring; no zoom-scale gimmicks.

**Key screens** (mocked in Paper): Library grid · Movie detail · Interactive release picker · Activity queue. The release picker is a first-class screen per SPEC §5.1 — a full-width table, not a modal.

## 6. Core components

- **Button:** primary (rust fill, dark text, `--radius-md`, 32px h), secondary (raised fill + border), ghost (text only), danger (red fill). Focus: 2px rust ring, 2px offset.
- **Badge:** 20px h, `--radius-sm`, mono 12px for quality/codec (`1080p`, `HEVC`, `DTS ⚠`), Inter for counts. Tint background + colored text, never solid fills.
- **Status dot:** 8px circle + label. The single vocabulary for item state everywhere.
- **Progress bar:** 4px track `--color-border`, rust fill; moss when complete, red when failed, dusk blue when seeding.
- **Table:** header row 12px caps on `--color-surface`, 40px body rows, hairline row dividers, hover `--color-raised`. Release names in mono truncate middle, not end (group tags matter).
- **Input/select:** `--color-raised` fill, `--color-border-strong` border, 36px h; rust border on focus.
- **Toast/banner:** tint background + semantic left bar; banners (engine unreachable, dirty eject, password nag) pin under the top bar and don't auto-dismiss.
- **Empty states:** every list ships one (per SPEC's visible-failure philosophy): icon, one sentence, one action.

## 7. Motion & a11y

- Motion: 120–160ms ease-out on hover/focus/expand; progress animates width only. No entrance choreography; `prefers-reduced-motion` kills all transitions.
- All interactive elements keyboard-reachable; focus ring as in §6; poster cards are single links with the title as accessible name; status conveyed by dot **and** text, never color alone.
- Contrast: all text tokens on their designated surfaces meet WCAG AA (the `--color-accent-text` split exists exactly for this).

## 8. Implementation notes (phase 1)

- Fonts self-hosted as woff2 in the SPA bundle (`go:embed`), `@font-face` in `app.css`. Three families, weights 400/500/600/700 only — the binary carries them everywhere, including offline portable mode.
- Svelte components mirror §6 one-to-one under `web/src/lib/components/` (`Button.svelte`, `Badge.svelte`, `StatusDot.svelte`, …). No component library dependency; the system above is small enough to own.
- Dark is default (`<html class="dark">` not required — dark values are the base tokens). A future light theme overrides tokens under a `.light` class via `@custom-variant`.
