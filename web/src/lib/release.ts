/**
 * Release picker logic (SPEC §9 step 4): the score the table sorts by, and the
 * flags that warn a user off a result without hiding it.
 *
 * Nothing here is a quality profile — profiles and cutoffs are phase 3. This is
 * the "sensible default order" for a human reading a fan-out of raw indexer
 * results, and the user can still grab any row.
 *
 * Pure — unit-tested in release.test.ts.
 */

import type { Release } from './api/types';
import type { Tone } from './status';

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

/**
 * Sort score for the picker, higher being better.
 *
 * Quality dominates, then source, then swarm health, with small bonuses for a
 * PROPER/REPACK. A danger flag (a cinema recording, a dead swarm) outweighs the
 * entire ladder, so a flagged result sorts below every clean one no matter what
 * it claims — which is precisely the claim not to trust.
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
  const penalty = releaseFlags(release).filter((f) => f.tone === 'danger').length * FLAG_PENALTY;

  return quality + source + swarm + proper - penalty;
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

/** Scene tags for burned-in subtitles, which cannot be turned off. */
const HARDCODED_RE = /\b(hc|hardcoded|korsub)\b/i;

/**
 * What is wrong with this release, worst first. An empty list means nothing
 * stood out — not that the release is good.
 */
export function releaseFlags(release: Release): ReleaseFlag[] {
  const flags: ReleaseFlag[] = [];
  const parsed = release.parsed;

  if (RECORDED_SOURCES.has(parsed.source)) {
    flags.push({
      key: 'cam',
      label: 'CAM',
      tone: 'danger',
      title: 'Recorded in a cinema — expect unwatchable video and audio.',
    });
  }

  if (release.protocol === 'torrent' && release.seeders <= 0) {
    flags.push({
      key: 'no-seeds',
      label: 'NO SEEDS',
      tone: 'danger',
      title: 'No seeders: this torrent has nobody to download from and will stall at 0%.',
    });
  }

  if (HARDCODED_RE.test(release.title)) {
    flags.push({
      key: 'hardcoded',
      label: 'HC SUBS',
      tone: 'warning',
      title: 'Hardcoded subtitles are burned into the picture and cannot be turned off.',
    });
  }

  if (parsed.audio.toUpperCase().includes('DTS')) {
    flags.push({
      key: 'dts',
      label: 'DTS',
      tone: 'warning',
      title: 'DTS audio: many TVs cannot decode it, so this may need converting for playback.',
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
 * Picker order: score descending, then seeders, then title, so two runs over
 * the same results always agree. The server sorts too; sorting here is what
 * makes the visible score column and the visible order the same fact.
 */
export function sortReleases(releases: readonly Release[]): Release[] {
  return [...releases].sort((a, b) => {
    const byScore = releaseScore(b) - releaseScore(a);
    if (byScore !== 0) return byScore;
    const bySeeders = b.seeders - a.seeders;
    if (bySeeders !== 0) return bySeeders;
    return a.title.localeCompare(b.title);
  });
}
