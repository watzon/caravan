/**
 * The request list (SPEC §11 `GET /requests`), shared by the requests screen
 * and the sidebar badge.
 *
 * The shell does not poll this. Local writes update the list immediately;
 * GET /events/stream tells another browser to refresh the snapshot.
 * subscribe() remains for tests and any screen that still wants a timer.
 */

import { api, errorText } from '../api/client';
import type { MediaRequest, RequestStatus } from '../api/types';
import { pendingRequestCount } from '../requests';

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
  #pending = false;

  /**
   * Fetch once. A second call while one is in flight is remembered and run
   * after, so an approve that finishes during a poll is not dropped.
   */
  async refresh(): Promise<void> {
    if (this.#inFlight) {
      this.#pending = true;
      return;
    }
    this.#inFlight = true;
    try {
      do {
        this.#pending = false;
        this.items = await api.listRequests();
        this.error = null;
      } while (this.#pending);
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

  /**
   * Put a request into the local list the moment it is created. A second
   * request for the same title merges into the pending row (the server's 201
   * is that row), so this replaces by id rather than appending.
   */
  remember(request: MediaRequest): void {
    const list = this.items ?? [];
    const index = list.findIndex((row) => row.id === request.id);
    this.items = index >= 0
      ? [...list.slice(0, index), request, ...list.slice(index + 1)]
      : [request, ...list];
  }

  /** Change one row's status without waiting for the next poll. */
  applyStatus(id: number, status: RequestStatus): void {
    if (this.items === null) return;
    this.items = this.items.map((row) => (row.id === id ? { ...row, status } : row));
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
