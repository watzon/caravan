import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import TasksSettings from './TasksSettings.svelte';
import type { SystemTask } from '../api/types';
import { toasts } from '../state/toast.svelte';

const NOW = Date.parse('2026-08-04T12:00:00Z');

/** A healthy task: one clean run behind it, the successor queued ahead. */
const RSS: SystemTask = {
  kind: 'rss_sync',
  name: 'RSS sync',
  description: 'Checks indexer feeds for newly posted releases.',
  interval_minutes: 15,
  last_run: new Date(NOW - 10 * 60_000).toISOString(),
  last_result: 'ok',
  last_error: '',
  next_run: new Date(NOW + 5 * 60_000).toISOString(),
  running: false,
  queued: true,
};

/** A task that has never finished, and whose next run is six hours out. */
const BACKLOG: SystemTask = {
  kind: 'backlog_sweep',
  name: 'Backlog search',
  description: 'Searches indexers for everything on the wanted list.',
  interval_minutes: 360,
  last_run: '',
  last_result: '',
  last_error: '',
  next_run: new Date(NOW + 6 * 60 * 60_000).toISOString(),
  running: false,
  queued: true,
};

/** A task whose last run failed, with the reason the API carried back. */
const REFRESH: SystemTask = {
  kind: 'refresh_metadata',
  name: 'Metadata refresh',
  description: 'Updates titles, statuses, new seasons and scenes.',
  interval_minutes: 720,
  last_run: new Date(NOW - 3 * 60 * 60_000).toISOString(),
  last_result: 'failed',
  last_error: 'tmdb unreachable',
  next_run: '',
  running: false,
  queued: true,
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

let host: HTMLElement;
let app: Record<string, unknown>;
/** What GET /system/tasks answers, per test. */
let tasks: SystemTask[];
/** What POST .../run answers, per test. */
let runResult: () => Response;
/** Every kind the Run now button posted for. */
let ran: string[];
/** When true the run request hangs until releaseRun is called. */
let deferRun: boolean;
let releaseRun: ((response: Response) => void) | null;

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
  tasks = [RSS, BACKLOG, REFRESH];
  ran = [];
  deferRun = false;
  releaseRun = null;
  runResult = () => jsonResponse({ kind: 'rss_sync', already_running: false });
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const run = /\/system\/tasks\/([^/]+)\/run$/.exec(url);
      if (run && init?.method === 'POST') {
        ran.push(run[1] ?? '');
        if (deferRun) return new Promise<Response>((resolve) => (releaseRun = resolve));
        return runResult();
      }
      if (url.endsWith('/system/tasks')) return jsonResponse({ tasks });
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

async function settle() {
  await vi.advanceTimersByTimeAsync(0);
  await Promise.resolve();
  flushSync();
}

async function render() {
  app = mount(TasksSettings, { target: host });
  await settle();
}

function row(name: string): HTMLElement {
  const found = [...host.querySelectorAll('li')].find((li) =>
    li.querySelector('.font-medium')?.textContent?.includes(name),
  );
  expect(found, `row for ${name}`).toBeDefined();
  return found as HTMLElement;
}

/** The name/description column, which also carries the Running badge and any
 * failure reason. */
function heading(name: string): string {
  return row(name).querySelector('div')?.textContent ?? '';
}

/** One labelled reading out of a row: Every, Last run, Next run. */
function field(name: string, label: string): string {
  const cell = [...row(name).querySelectorAll('dl > div')].find(
    (d) => d.querySelector('dt')?.textContent?.trim() === label,
  );
  expect(cell, `${label} cell in the ${name} row`).toBeDefined();
  return cell?.querySelector('dd')?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
}

function runButton(name: string): HTMLButtonElement {
  const found = [...row(name).querySelectorAll('button')].find((b) =>
    /Run now|Starting/.test(b.textContent ?? ''),
  );
  expect(found, `Run now button in the ${name} row`).toBeDefined();
  return found as HTMLButtonElement;
}

describe('TasksSettings', () => {
  it('renders one row per task with its cadence, last run and next run', async () => {
    await render();

    expect(host.querySelectorAll('li')).toHaveLength(3);
    expect(heading('RSS sync')).toContain('Checks indexer feeds');
    expect(field('RSS sync', 'Every')).toBe('15 min');
    expect(field('RSS sync', 'Last run')).toBe('10m ago OK');
    expect(field('RSS sync', 'Next run')).toBe('in 5m');

    expect(field('Backlog search', 'Every')).toBe('6 h');
    expect(field('Backlog search', 'Next run')).toBe('in 6h');
    // Nothing has finished yet: no age, and no result badge to colour.
    expect(field('Backlog search', 'Last run')).toBe('Never');
  });

  it('shows why the last run failed rather than only that it did', async () => {
    await render();
    expect(field('Metadata refresh', 'Last run')).toContain('Failed');
    expect(heading('Metadata refresh')).toContain('tmdb unreachable');
    // A queued task with no next_run is waiting on the next poll, not unscheduled.
    expect(field('Metadata refresh', 'Next run')).toBe('now');
  });

  it('marks a running task and offers no countdown for it', async () => {
    tasks = [{ ...RSS, running: true, next_run: '' }];
    await render();
    expect(heading('RSS sync')).toContain('Running');
    expect(field('RSS sync', 'Next run')).toBe('Running now');
  });

  it('says so when nothing is queued at all', async () => {
    tasks = [{ ...RSS, queued: false, next_run: '' }];
    await render();
    expect(field('RSS sync', 'Next run')).toBe('Not scheduled');
  });

  it('runs a task on demand, busying only that row while it does', async () => {
    await render();
    deferRun = true;

    runButton('RSS sync').click();
    await settle();

    expect(ran).toEqual(['rss_sync']);
    expect(runButton('RSS sync').textContent).toContain('Starting…');
    expect(runButton('RSS sync').disabled).toBe(true);
    // The other rows are untouched: the busy state is per task, not per screen.
    expect(runButton('Backlog search').disabled).toBe(false);

    deferRun = false;
    releaseRun?.(jsonResponse({ kind: 'rss_sync', already_running: false }));
    await settle();

    expect(runButton('RSS sync').disabled).toBe(false);
    expect(toasts.items.at(-1)?.message).toContain('RSS sync');
    expect(toasts.items.at(-1)?.tone).toBe('success');
  });

  it('reports an already-running task as information, not success', async () => {
    await render();
    runResult = () => jsonResponse({ kind: 'rss_sync', already_running: true });

    runButton('RSS sync').click();
    await settle();

    expect(toasts.items.at(-1)?.message).toBe('RSS sync is already running.');
    expect(toasts.items.at(-1)?.tone).toBe('info');
  });

  it('surfaces a refused run as an error toast', async () => {
    await render();
    runResult = () => jsonResponse({ error: 'unknown task' }, 404);

    runButton('RSS sync').click();
    await settle();

    expect(toasts.items.at(-1)?.message).toContain('unknown task');
    expect(toasts.items.at(-1)?.tone).toBe('danger');
  });

  it('polls so a task started elsewhere shows up without a reload', async () => {
    await render();
    expect(heading('RSS sync')).not.toContain('Running');

    tasks = [{ ...RSS, running: true, next_run: '' }, BACKLOG, REFRESH];
    await vi.advanceTimersByTimeAsync(5000);
    flushSync();

    expect(heading('RSS sync')).toContain('Running');
  });
});
