/**
 * Rendering the TV-profile verdict (SPEC §8).
 *
 * The verdict itself is the server's — it knows which profile is active and
 * owns the capability tables (internal/core/tvprofile.go). This module only
 * decides what a human sees: a badge for the two states worth acting on, and
 * nothing at all for "fine" or "cannot tell". A badge that appears on every
 * row teaches the user to ignore badges.
 *
 * Pure — unit-tested in tvcompat.test.ts.
 */

import type { TVCompatibility } from './api/types';
import type { Tone } from './status';

/** One compatibility badge, shaped like the picker's other flag badges. */
export interface CompatBadge {
  key: string;
  /** Badge text — mono, so it stays short and machine-looking. */
  label: string;
  tone: Tone;
  /** The whole reason, on hover: a badge alone never explains itself. */
  title: string;
}

/**
 * The badge for a verdict, or null when there is nothing to say.
 *
 * "compatible" and "unknown" both render nothing, and deliberately so: the
 * first needs no warning and the second has no evidence. Neither is a claim
 * the UI should make on the user's behalf.
 */
export function compatBadge(compat: TVCompatibility | undefined | null): CompatBadge | null {
  if (!compat) return null;
  const reasons = compat.reasons?.length ? ` ${compat.reasons.join('; ')}.` : '';

  switch (compat.verdict) {
    case 'incompatible':
      return {
        key: 'tv-incompatible',
        label: 'NEEDS CONVERT',
        tone: 'warning',
        title: `Will not play natively on your TV profile — the video or audio has to be re-encoded.${reasons}`,
      };
    case 'needs-remux':
      return {
        key: 'tv-remux',
        label: 'NEEDS CONVERT',
        tone: 'warning',
        title: `The streams are fine; only the container needs conversion, so no re-encoding is required.${reasons}`,
      };
    default:
      return null;
  }
}
