/**
 * Convert-for-TV vocabulary (SPEC §8), mapped onto the one status palette the
 * rest of the UI already uses, so a running conversion looks like a running
 * download and a failed one looks like a failed download.
 *
 * Pure — unit-tested in conversion.test.ts.
 */

import type { Conversion, ConversionStatus, ConversionStrategy, TVCompatibility } from './api/types';
import type { Tone } from './status';

export interface ConversionStateMeta {
  label: string;
  tone: Tone;
  /** True while the queue is still going to act on it. */
  active: boolean;
}

export const CONVERSION_STATES: Record<ConversionStatus, ConversionStateMeta> = {
  queued: { label: 'Queued', tone: 'neutral', active: true },
  running: { label: 'Converting', tone: 'accent', active: true },
  done: { label: 'Done', tone: 'success', active: false },
  failed: { label: 'Failed', tone: 'danger', active: false },
  cancelled: { label: 'Cancelled', tone: 'neutral', active: false },
};

/**
 * Meta for a status, tolerating one the server invents later: an unknown
 * status renders neutrally rather than crashing the screen.
 */
export function conversionStateMeta(status: string): ConversionStateMeta {
  return (
    CONVERSION_STATES[status as ConversionStatus] ?? {
      label: status || 'Unknown',
      tone: 'neutral',
      active: false,
    }
  );
}

/**
 * How the strategy reads to a human. It is deliberately explicit about cost:
 * SPEC §8 makes the remux the cheap default and the transcode the slow
 * fallback, and a user deciding whether to wait needs to be told which is
 * which.
 */
export const CONVERSION_STRATEGIES: Record<Exclude<ConversionStrategy, ''>, string> = {
  none: 'Nothing to do',
  remux: 'Convert (stream copy)',
  transcode: 'Transcode (re-encode)',
};

export function strategyLabel(strategy: string): string {
  if (!strategy) return 'Deciding…';
  return CONVERSION_STRATEGIES[strategy as Exclude<ConversionStrategy, ''>] ?? strategy;
}

/**
 * Whether a file is worth offering a conversion for.
 *
 * "compatible" needs nothing and "unknown" has no evidence — converting on a
 * guess re-encodes a file that may well have been fine, which is the one
 * outcome that costs quality for nothing. This is the same rule
 * tvcompat.compatBadge uses to decide whether to say anything at all.
 */
export function convertible(compat: TVCompatibility | undefined | null): boolean {
  return compat?.verdict === 'needs-remux' || compat?.verdict === 'incompatible';
}

/** Conversions the queue is still going to act on. */
export function activeConversions(rows: readonly Conversion[]): Conversion[] {
  return rows.filter((row) => conversionStateMeta(row.status).active);
}

/** The open conversion for a file, if the queue holds one. */
export function openConversionFor(
  rows: readonly Conversion[],
  mediaFileID: number,
): Conversion | null {
  return (
    rows.find(
      (row) => row.media_file_id === mediaFileID && conversionStateMeta(row.status).active,
    ) ?? null
  );
}
