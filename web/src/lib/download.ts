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
import { translate, translatePlural } from './i18n.svelte';

export interface DownloadStateMeta {
  label: string;
  tone: Tone;
  /**
   * True while the engine is still transferring — downloading or seeding —
   * so the queue can offer pause. The sidebar badge does not use this: a
   * seeder is no longer work the user is waiting on.
   */
  active: boolean;
}

export const DOWNLOAD_STATES: Record<DownloadState, DownloadStateMeta> = {
  queued: { get label() { return translate('download.state.queued'); }, tone: 'neutral', active: true },
  downloading: { get label() { return translate('download.state.downloading'); }, tone: 'accent', active: true },
  seeding: { get label() { return translate('download.state.seeding'); }, tone: 'info', active: true },
  completed: { get label() { return translate('download.state.completed'); }, tone: 'success', active: false },
  failed: { get label() { return translate('download.state.failed'); }, tone: 'danger', active: false },
  paused: { get label() { return translate('download.state.paused'); }, tone: 'warning', active: false },
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
  get repairing() { return translate('download.phase.repairing'); },
  get extracting() { return translate('download.phase.extracting'); },
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
      label: state || translate('download.state.unknown'),
      tone: 'neutral',
      active: false,
    }
  );
}

/**
 * What the sidebar badge and the Active tab count: downloads still being
 * acquired. Seeding is a different state — the file is already here — so it
 * does not keep the badge lit for days. Paused is excluded for the same
 * reason: a paused download waits on the user, not on the engine.
 */
export function isActiveDownload(status: DownloadStatus): boolean {
  return status.state === 'queued' || status.state === 'downloading';
}

/** A torrent whose download finished and that is still uploading. */
export function isSeedingDownload(status: DownloadStatus): boolean {
  return status.state === 'seeding';
}

export function countActiveDownloads(downloads: readonly DownloadStatus[]): number {
  return downloads.filter(isActiveDownload).length;
}

/**
 * A download nothing more will happen to without user input: imported and
 * completed, or a torrent that finished its download and sits paused rather
 * than seeding. The queue's default view hides these — they are history, not
 * work — while a mid-download pause stays visible because it is waiting on
 * the user. Seeding has its own tab: the transfer is done, the upload is not.
 */
export function isFinishedDownload(status: DownloadStatus): boolean {
  return status.state === 'completed' || (status.state === 'paused' && status.progress >= 1);
}

/**
 * Whether the queue offers Retry for this download.
 *
 * Only a failed Usenet download: it is several stages — fetch, repair, unpack —
 * and a failure belongs to one of them, so trying again resumes from the stage
 * that broke rather than refetching gigabytes. A torrent has no such structure
 * and its engine says so by not implementing the capability at all, which is
 * why the protocol is the test here rather than a separate flag.
 *
 * The server is still the authority: it answers 400 for an engine that cannot
 * retry and 409 for a download that is no longer failed, and the click surfaces
 * whichever it gets.
 */
export function canRetryDownload(status: DownloadStatus): boolean {
  return status.state === 'failed' && status.protocol === 'usenet';
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
  if (engine === DEFAULT_ENGINE) return translate('download.engine.embedded');
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
  const reasons = [...new Set(clients.map((c) => c.error).filter(Boolean))].join('; ');
  const detail = reasons ? `${reasons}. ` : '';
  return {
    title: translatePlural('download.client.unreachable', clients.length, { names }),
    message: translatePlural('download.client.unreachable.message', clients.length, { detail }),
  };
}


/**
 * Queue order: most recently added first. State changes do not move a row
 * between polls; an engine-only row with no persisted creation time stays
 * behind the persisted queue in the order the server supplied it.
 */
export function sortDownloads(downloads: readonly DownloadStatus[]): DownloadStatus[] {
  return [...downloads].sort((a, b) => b.created_at.localeCompare(a.created_at));
}
