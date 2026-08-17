/**
 * Recurring tasks and live one-shot jobs, shared by the sidebar footer and
 * the Settings badge.
 *
 * The shell does not poll this. Local writes and GET /events/stream refresh
 * the snapshot. subscribe() remains for the Tasks screen and watchSoon.
 */

import { api, errorText } from '../api/client';
import type { Job, SystemTask } from '../api/types';
import { failedTaskCount, footerActivity } from '../tasks';

/** Tasks screen and a just-queued search. */
export const TASKS_POLL_MS = 5000;

/** How long a just-queued search is watched at the fast rate. */
export const TASKS_WATCH_SOON_MS = 30_000;

class TasksState {
  tasks = $state<SystemTask[] | null>(null);
  jobs = $state<Job[] | null>(null);
  error = $state<string | null>(null);
  loading = $state(true);

  get activity() {
    return footerActivity(this.tasks, this.jobs);
  }

  get issueCount(): number {
    return failedTaskCount(this.tasks);
  }

  #subscribers = new Map<number, number>();
  #nextToken = 1;
  #timer: ReturnType<typeof setInterval> | null = null;
  #watchingVisibility = false;
  #inFlight = false;
  #pending = false;
  #soonStop: (() => void) | null = null;
  #soonTimer: ReturnType<typeof setTimeout> | null = null;

  async refresh(): Promise<void> {
    if (this.#inFlight) {
      this.#pending = true;
      return;
    }
    this.#inFlight = true;
    try {
      do {
        this.#pending = false;
        const [taskList, jobList] = await Promise.all([
          api.listTasks(),
          api.listJobs(50),
        ]);
        this.tasks = taskList;
        this.jobs = jobList;
        this.error = null;
      } while (this.#pending);
    } catch (err) {
      this.error = errorText(err);
    } finally {
      this.#inFlight = false;
      this.loading = false;
    }
  }

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

  watchSoon(durationMs = TASKS_WATCH_SOON_MS): void {
    void this.refresh();
    this.stopSoon();
    this.#soonStop = this.subscribe(TASKS_POLL_MS);
    this.#soonTimer = setTimeout(() => this.stopSoon(), durationMs);
  }

  stopSoon(): void {
    if (this.#soonTimer !== null) {
      clearTimeout(this.#soonTimer);
      this.#soonTimer = null;
    }
    if (this.#soonStop) {
      this.#soonStop();
      this.#soonStop = null;
    }
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
      if (!document.hidden && this.#subscribers.size > 0) void this.refresh();
      this.#restart();
    });
  }
}

export const tasks = new TasksState();
