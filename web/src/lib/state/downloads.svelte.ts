/**
 * The download queue, polled (SPEC §11 `GET /downloads`).
 *
 * Two screens want this at different rates: the queue table needs it every few
 * seconds to animate progress, the sidebar badge is happy with a lazy count.
 * Rather than run two pollers, subscribers declare the interval they need and
 * the store polls at the fastest one currently asked for, and not at all when
 * nobody is asking or the tab is in the background.
 */

import { api, errorText } from '../api/client';
import type { DownloadStatus } from '../api/types';
import { countActiveDownloads } from '../download';

/** Queue screen cadence — fast enough that progress bars move (DESIGN.md §6). */
export const QUEUE_POLL_MS = 3000;

/** Sidebar badge cadence: a count that is a few seconds stale costs nothing. */
export const BADGE_POLL_MS = 15000;

class DownloadsState {
  items = $state<DownloadStatus[] | null>(null);
  error = $state<string | null>(null);
  loading = $state(true);

  /** What the sidebar badge shows. */
  get activeCount(): number {
    return countActiveDownloads(this.items ?? []);
  }

  #subscribers = new Map<number, number>();
  #nextToken = 1;
  #timer: ReturnType<typeof setInterval> | null = null;
  #watchingVisibility = false;
  #inFlight = false;

  /**
   * Fetch once. Overlapping calls are dropped rather than queued: a slow
   * response must not pile up a backlog of identical requests.
   */
  async refresh(): Promise<void> {
    if (this.#inFlight) return;
    this.#inFlight = true;
    try {
      const items: DownloadStatus[] = [];
      let cursor: string | undefined;
      do {
        const page = await api.listDownloadsPage(100, cursor);
        items.push(...page.downloads);
        cursor = page.next_cursor || undefined;
      } while (cursor);
      this.items = items;
      this.error = null;
    } catch (err) {
      this.error = errorText(err);
    } finally {
      this.#inFlight = false;
      this.loading = false;
    }
  }

  /**
   * Poll at least this often while the returned function has not been called.
   * Returns the unsubscribe.
   */
  subscribe(intervalMs: number): () => void {
    const token = this.#nextToken++;
    this.#subscribers.set(token, intervalMs);
    this.#watchVisibility();
    void this.refresh();
    this.#restart();

    return () => {
      this.#subscribers.delete(token);
      this.#restart();
    };
  }

  /** Drop a download from the local list without waiting for the next poll. */
  forget(id: string): void {
    if (this.items === null) return;
    this.items = this.items.filter((d) => d.id !== id);
  }

  #restart(): void {
    if (this.#timer !== null) {
      clearInterval(this.#timer);
      this.#timer = null;
    }
    if (this.#subscribers.size === 0) return;
    if (typeof document !== 'undefined' && document.hidden) return;

    const interval = Math.min(...this.#subscribers.values());
    this.#timer = setInterval(() => void this.refresh(), interval);
  }

  #watchVisibility(): void {
    if (this.#watchingVisibility || typeof document === 'undefined') return;
    this.#watchingVisibility = true;
    document.addEventListener('visibilitychange', () => {
      // Coming back to a stale queue should not wait out a full interval.
      if (!document.hidden && this.#subscribers.size > 0) void this.refresh();
      this.#restart();
    });
  }
}

export const downloads = new DownloadsState();
