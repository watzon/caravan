/**
 * Release picker logic (SPEC §9 step 4): the score the table sorts by, and the
 * flags that warn a user off a result without hiding it.
 *
 * Nothing here is a quality profile. This is the "sensible default order" for a
 * human reading a fan-out of raw indexer results, and the user can still grab
 * any row.
 *
 * Pure — unit-tested in release.test.ts.
 */

import type { Release } from './api/types';
import type { Tone } from './status';
import { translate } from './i18n.svelte';

/** Quality ladder, best first — mirrors internal/core.QualityLadder. */
export const QUALITY_LADDER = ['2160p', '1080p', '720p', '480p'] as const;

/** Source ladder, best first — mirrors internal/core.SourceLadder. */
export const SOURCE_LADDER = [
  'bluray',
  'webdl',
  'webrip',
  'hdtv',
  'dvd',
  'cam',
] as const;

/**
 * Rank in a ladder, lower being better; anything unrecognized ranks below every
 * known rung, exactly like core.QualityRank/core.SourceRank.
 */
function rank(ladder: readonly string[], value: string): number {
  const i = ladder.indexOf(value);
  return i === -1 ? ladder.length : i;
}

/**
 * Points a release loses for each danger flag it carries. Larger than the whole
 * quality ladder plus a perfect swarm is worth, so anything flagged sinks below
 * everything clean — a CAM claiming 2160p is still a CAM.
 */
const FLAG_PENALTY = 1000;

/** Larger than the entire quality ladder, so the score reflects incompatibility. */
const INCOMPATIBILITY_PENALTY = 1000;

/**
 * Sort score for the picker, higher being better.
 *
 * Quality dominates, then source, then swarm health, with small bonuses for a
 * PROPER/REPACK. A danger flag or active-profile incompatibility outweighs the
 * entire quality ladder, so a flagged or incompatible result scores below a
 * clean, compatible one no matter what it claims.
 */
export function releaseScore(release: Release): number {
  const parsed = release.parsed;

  const quality = (QUALITY_LADDER.length - rank(QUALITY_LADDER, parsed.quality)) * 200;
  const source = (SOURCE_LADDER.length - rank(SOURCE_LADDER, parsed.source)) * 30;

  // Seeders matter a lot at 0-10 and barely at all past 100, which is what a
  // log curve says and a linear term does not.
  const swarm =
    release.protocol === 'torrent'
      ? Math.round(Math.log10(Math.max(0, release.seeders) + 1) * 60)
      : 0;

  const proper = (parsed.proper ? 25 : 0) + (parsed.repack ? 25 : 0);
  const flagPenalty =
    releaseFlags(release).filter((f) => f.tone === 'danger').length * FLAG_PENALTY;
  const incompatibilityPenalty =
    release.compatibility.verdict === 'incompatible' ? INCOMPATIBILITY_PENALTY : 0;

  return quality + source + swarm + proper - flagPenalty - incompatibilityPenalty;
}

/** One warning badge on a release row. */
export interface ReleaseFlag {
  key: string;
  /** Badge text — mono, so it stays short and machine-looking. */
  label: string;
  tone: Tone;
  /** The whole reason, on hover: a badge alone never explains itself. */
  title: string;
}

/** Sources that are a recording of a screen, not a copy of a master. */
const RECORDED_SOURCES = new Set(['cam']);

/**
 * Server-computed mismatch flags worth a badge, keyed by the wire value.
 * "no-seeders" is deliberately absent — the client already derives NO SEEDS
 * from the seeder count and a duplicate badge would say nothing new.
 */
const SERVER_FLAGS: Record<
  string,
  { labelKey: Parameters<typeof translate>[0]; titleKey: Parameters<typeof translate>[0]; tone: Tone }
> = {
  'wrong-title': {
    labelKey: 'release.flag.wrongTitle.label',
    titleKey: 'release.flag.wrongTitle.title',
    tone: 'danger',
  },
  'wrong-year': {
    labelKey: 'release.flag.wrongYear.label',
    titleKey: 'release.flag.wrongYear.title',
    tone: 'warning',
  },
  'wrong-season': {
    labelKey: 'release.flag.wrongSeason.label',
    titleKey: 'release.flag.wrongSeason.title',
    tone: 'warning',
  },
  'wrong-episode': {
    labelKey: 'release.flag.wrongEpisode.label',
    titleKey: 'release.flag.wrongEpisode.title',
    tone: 'warning',
  },
  'wrong-date': {
    labelKey: 'release.flag.wrongDate.label',
    titleKey: 'release.flag.wrongDate.title',
    tone: 'warning',
  },
  'season-pack': {
    labelKey: 'release.flag.seasonPack.label',
    titleKey: 'release.flag.seasonPack.title',
    tone: 'info',
  },
};

/** Scene tags for burned-in subtitles, which cannot be turned off. */
const HARDCODED_RE = /\b(hc|hardcoded|korsub)\b/i;

/**
 * What is wrong with this release, worst first. An empty list means nothing
 * stood out — not that the release is good.
 */
export function releaseFlags(release: Release): ReleaseFlag[] {
  const flags: ReleaseFlag[] = [];
  const parsed = release.parsed;

  for (const key of release.flags ?? []) {
    const known = SERVER_FLAGS[key];
    if (!known) continue;
    flags.push({
      key,
      label: translate(known.labelKey),
      tone: known.tone,
      title: translate(known.titleKey),
    });
  }

  if (RECORDED_SOURCES.has(parsed.source)) {
    flags.push({
      key: 'cam',
      label: translate('release.flag.cam.label'),
      tone: 'danger',
      title: translate('release.flag.cam.title'),
    });
  }

  if (release.protocol === 'torrent' && release.seeders <= 0) {
    flags.push({
      key: 'no-seeds',
      label: translate('release.flag.noSeeds.label'),
      tone: 'danger',
      title: translate('release.flag.noSeeds.title'),
    });
  }

  if (HARDCODED_RE.test(release.title)) {
    flags.push({
      key: 'hardcoded',
      label: translate('release.flag.hardcodedSubs.label'),
      tone: 'warning',
      title: translate('release.flag.hardcodedSubs.title'),
    });
  }

  if (parsed.audio.toUpperCase().includes('DTS')) {
    flags.push({
      key: 'dts',
      label: translate('release.flag.dts.label'),
      tone: 'warning',
      title: translate('release.flag.dts.title'),
    });
  }

  return flags;
}

/**
 * True when a release carries a flag serious enough to steer a user away from
 * it. Flagged rows are de-emphasized in the picker, never hidden or disabled —
 * sometimes the CAM is the only copy that exists (SPEC §13).
 */
export function isFlagged(release: Release): boolean {
  return releaseFlags(release).some((f) => f.tone === 'danger');
}

/**
 * Picker order: incompatible releases last, then score descending, seeders, and
 * title, so two runs over the same results always agree. The server sorts too;
 * sorting here keeps the visible score column and visible order aligned.
 */
export function sortReleases(releases: readonly Release[]): Release[] {
  return [...releases].sort((a, b) => {
    const byCompatibility =
      Number(a.compatibility.verdict === 'incompatible') -
      Number(b.compatibility.verdict === 'incompatible');
    if (byCompatibility !== 0) return byCompatibility;
    // The server sinks wrong-title matches; the client re-sort must agree
    // or its visible order would fight the server's.
    const byMismatch =
      Number((a.flags ?? []).includes('wrong-title')) -
      Number((b.flags ?? []).includes('wrong-title'));
    if (byMismatch !== 0) return byMismatch;
    const byScore = releaseScore(b) - releaseScore(a);
    if (byScore !== 0) return byScore;
    const bySeeders = b.seeders - a.seeders;
    if (bySeeders !== 0) return bySeeders;
    return a.title.localeCompare(b.title);
  });
}
