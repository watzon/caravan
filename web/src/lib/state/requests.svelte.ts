/**
 * The request list (SPEC §11 `GET /requests`), shared by the requests screen
 * and the sidebar badge.
 *
 * Same shape as the downloads store: subscribers declare the interval they
 * need and the store polls at the fastest one currently asked for, so the badge
 * staying live does not make the shell chatty while the screen is open.
 */

import { api, errorText } from '../api/client';
import type { MediaRequest } from '../api/types';
import { pendingRequestCount } from '../requests';

/** Requests screen cadence — an approval elsewhere shows up quickly. */
export const REQUESTS_POLL_MS = 15000;

/** Sidebar badge cadence: a count a minute stale costs nothing. */
export const REQUESTS_BADGE_POLL_MS = 60000;

class RequestsState {
  items = $state<MediaRequest[] | null>(null);
  error = $state<string | null>(null);
  loading = $state(true);

  /** What the sidebar badge shows. */
  get pendingCount(): number {
    return pendingRequestCount(this.items);
  }

  #subscribers = new Map<number, number>();
  #nextToken = 1;
  #timer: ReturnType<typeof setInterval> | null = null;
  #watchingVisibility = false;
  #inFlight = false;

  /** Fetch once. Overlapping calls are dropped rather than queued. */
  async refresh(): Promise<void> {
    if (this.#inFlight) return;
    this.#inFlight = true;
    try {
      this.items = await api.listRequests();
      this.error = null;
    } catch (err) {
      this.error = errorText(err);
    } finally {
      this.#inFlight = false;
      this.loading = false;
    }
  }

  /** Poll at least this often until the returned unsubscribe is called. */
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

  /** Drop a row locally after it stops being pending, without waiting a poll. */
  forget(id: number): void {
    if (this.items === null) return;
    this.items = this.items.filter((r) => r.id !== id);
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

  /**
   * The hidden-tab early return in #restart() is only half the rule: without
   * this listener, a shell that mounts in a background tab never starts a
   * timer and never recovers, so the badge freezes at its mount-time count for
   * the whole session.
   */
  #watchVisibility(): void {
    if (this.#watchingVisibility || typeof document === 'undefined') return;
    this.#watchingVisibility = true;
    document.addEventListener('visibilitychange', () => {
      // Coming back to a stale badge should not wait out a full interval.
      if (!document.hidden && this.#subscribers.size > 0) void this.refresh();
      this.#restart();
    });
  }
}

export const requests = new RequestsState();
