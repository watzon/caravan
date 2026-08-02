/**
 * Queue vocabulary (SPEC §5.1): one mapping from internal/core.DownloadState to
 * the status colours the rest of the UI already uses, so a downloading item
 * looks like a wanted item and a failed download looks like a missing file.
 *
 * Pure — unit-tested in download.test.ts.
 */

import type {
  DownloadClientType,
  DownloadPhase,
  DownloadState,
  DownloadStatus,
  UnhealthyDownloadClient,
} from './api/types';
import { FALLBACK_DOWNLOAD_CLIENT_TYPES } from './downloadClient';
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
 * What each sub-step is called on screen.
 *
 * "Downloading" is deliberately absent: while a download is fetching articles
 * the phase says exactly what the state badge beside it already says, and two
 * badges reading "Downloading" is noise. The phase badge only appears once the
 * engine is doing something the state cannot express.
 */
export const DOWNLOAD_PHASES: Record<DownloadPhase, string> = {
  downloading: '',
  repairing: 'Repairing',
  extracting: 'Extracting',
};

/**
 * The label for a download's current sub-step, or "" when there is nothing
 * worth showing. Tolerates a phase the server invents later by titlecasing it,
 * so a new stage shows up rather than disappearing.
 */
export function downloadPhaseLabel(status: DownloadStatus): string {
  const phase = status.phase;
  if (!phase) return '';
  const known = DOWNLOAD_PHASES[phase as DownloadPhase];
  if (known !== undefined) return known;
  return phase.charAt(0).toUpperCase() + phase.slice(1);
}

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

/** The built-in torrent engine, and the answer when a row names no backend. */
export const DEFAULT_ENGINE = 'embedded';

/**
 * Which backend holds this download, as the queue row and detail drawer label
 * it. The server records the backend's own name ("qbittorrent"); the download
 * client type table already knows how humans spell those, so the row says
 * "qBittorrent" rather than repeating a database value at the user.
 */
export function engineLabel(status: DownloadStatus): string {
  const engine = status.engine || DEFAULT_ENGINE;
  if (engine === DEFAULT_ENGINE) return 'Embedded';
  return (
    FALLBACK_DOWNLOAD_CLIENT_TYPES.find((t) => t.type === (engine as DownloadClientType))?.label ??
    engine
  );
}

/**
 * The "client X unreachable" banner's text (SPEC §5.1, §13).
 *
 * Null when everything is answering, which is the normal case. The message
 * names every client that is down and says what that means for the queue,
 * because a banner that only says "unreachable" leaves the user wondering
 * whether their other downloads are still running. They are: the embedded
 * engine and every other client are untouched.
 */
export function unreachableClientBanner(
  clients: readonly UnhealthyDownloadClient[] | undefined,
): { title: string; message: string } | null {
  if (!clients || clients.length === 0) return null;
  const names = clients.map((c) => c.name).join(', ');
  const title =
    clients.length === 1
      ? `Download client ${names} is unreachable`
      : `Download clients ${names} are unreachable`;
  const reasons = [...new Set(clients.map((c) => c.error).filter(Boolean))].join('; ');
  const detail = reasons ? `${reasons}. ` : '';
  return {
    title,
    message:
      `${detail}Its queue has stopped updating and new grabs routed to it are refused until it ` +
      'answers again. Everything else — the built-in engine and any other client — is unaffected.',
  };
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
