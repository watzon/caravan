/**
 * Convert-for-TV vocabulary (SPEC §8), mapped onto the one status palette the
 * rest of the UI already uses, so a running conversion looks like a running
 * download and a failed one looks like a failed download.
 *
 * Pure — unit-tested in conversion.test.ts.
 */

import type { Conversion, ConversionStatus, ConversionStrategy, TVCompatibility } from './api/types';
import type { Tone } from './status';
import { translate } from './i18n.svelte';

export interface ConversionStateMeta {
  label: string;
  tone: Tone;
  /** True while the queue is still going to act on it. */
  active: boolean;
}

export const CONVERSION_STATES: Record<ConversionStatus, ConversionStateMeta> = {
  queued: { get label() { return translate('conversion.state.queued'); }, tone: 'neutral', active: true },
  running: { get label() { return translate('conversion.state.running'); }, tone: 'accent', active: true },
  done: { get label() { return translate('conversion.state.done'); }, tone: 'success', active: false },
  failed: { get label() { return translate('conversion.state.failed'); }, tone: 'danger', active: false },
  cancelled: { get label() { return translate('conversion.state.cancelled'); }, tone: 'neutral', active: false },
};

/**
 * Meta for a status, tolerating one the server invents later: an unknown
 * status renders neutrally rather than crashing the screen.
 */
export function conversionStateMeta(status: string): ConversionStateMeta {
  return (
    CONVERSION_STATES[status as ConversionStatus] ?? {
      label: status || translate('conversion.state.unknown'),
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
  get none() { return translate('conversion.strategy.none'); },
  get remux() { return translate('conversion.strategy.remux'); },
  get transcode() { return translate('conversion.strategy.transcode'); },
};

export function strategyLabel(strategy: string): string {
  if (!strategy) return translate('conversion.strategy.deciding');
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
