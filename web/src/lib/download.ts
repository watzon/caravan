/**
 * Queue vocabulary (SPEC §5.1): one mapping from internal/core.DownloadState to
 * the status colours the rest of the UI already uses, so a downloading item
 * looks like a wanted item and a failed download looks like a missing file.
 *
 * Pure — unit-tested in download.test.ts.
 */

import type { DownloadState, DownloadStatus } from './api/types';
import type { Tone } from './status';

export interface DownloadStateMeta {
  label: string;
  tone: Tone;
  /** True while the engine is still working on it, or still seeding it. */
  active: boolean;
}

export const DOWNLOAD_STATES: Record<DownloadState, DownloadStateMeta> = {
  queued: { label: 'Queued', tone: 'neutral', active: true },
  downloading: { label: 'Downloading', tone: 'accent', active: true },
  seeding: { label: 'Seeding', tone: 'info', active: true },
  completed: { label: 'Completed', tone: 'success', active: false },
  failed: { label: 'Failed', tone: 'danger', active: false },
  paused: { label: 'Paused', tone: 'warning', active: false },
};

/**
 * Meta for a state, tolerating a state string the server invents later: an
 * unknown state renders neutrally rather than crashing the queue.
 */
export function downloadStateMeta(state: string): DownloadStateMeta {
  return (
    DOWNLOAD_STATES[state as DownloadState] ?? {
      label: state || 'Unknown',
      tone: 'neutral',
      active: false,
    }
  );
}

/**
 * What the sidebar badge counts: downloads the engine is still doing something
 * about. Paused is deliberately excluded — a paused download is waiting on the
 * user, not on the engine, and a badge that never clears is a badge nobody
 * reads.
 */
export function isActiveDownload(status: DownloadStatus): boolean {
  return downloadStateMeta(status.state).active;
}

export function countActiveDownloads(downloads: readonly DownloadStatus[]): number {
  return downloads.filter(isActiveDownload).length;
}

/** Which engine holds this download; phase 2 ships exactly one. */
export const DEFAULT_ENGINE = 'embedded';

export function engineLabel(status: DownloadStatus): string {
  return status.engine || DEFAULT_ENGINE;
}

/**
 * Queue order: active work first, then failures (they need attention), then the
 * finished pile, alphabetically inside each group so rows stop jumping between
 * polls.
 */
const STATE_ORDER: DownloadState[] = [
  'downloading',
  'queued',
  'seeding',
  'paused',
  'failed',
  'completed',
];

export function sortDownloads(downloads: readonly DownloadStatus[]): DownloadStatus[] {
  const weight = (s: string) => {
    const i = STATE_ORDER.indexOf(s as DownloadState);
    return i === -1 ? STATE_ORDER.length : i;
  };
  return [...downloads].sort((a, b) => {
    const byState = weight(a.state) - weight(b.state);
    if (byState !== 0) return byState;
    return a.name.localeCompare(b.name);
  });
}
