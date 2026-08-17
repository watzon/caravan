/**
 * The sidebar's task rail and the Settings badge that points at it.
 *
 * Background work used to toast; it now lives here. These assert on rendered
 * text on purpose: `npm run check` type-checks the script blocks and not the
 * templates, so a mistyped prop or a row that never renders is a silent pass
 * everywhere else.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Sidebar from './Sidebar.svelte';
import type { Job, SystemStatus, SystemTask } from '../api/types';
import { saveDisplayPreferences } from '../displayPreferences';
import { session } from '../state/session.svelte';
import { system } from '../state/system.svelte';
import { tasks } from '../state/tasks.svelte';

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

function seedStatus(fields: Partial<SystemStatus> = {}): void {
  system.status = {
    version: '0.1.0',
    mode: 'server',
    storage_root: '/data',
    schema_version: 1,
    scanning: false,
    counts: { movies: 0, series: 0, media_files: 0, unmatched: 0 },
    disk_free_bytes: 500 * 1024 ** 3,
    disk_total_bytes: 1024 ** 4,
    engine_health: 'ok',
    ffmpeg_available: true,
    ...fields,
  } as SystemStatus;
  system.loading = false;
}

function task(extra: Partial<SystemTask> = {}): SystemTask {
  return {
    kind: 'rss_sync',
    name: 'RSS sync',
    description: 'Checks indexer feeds for newly posted releases.',
    interval_minutes: 15,
    last_run: '',
    last_result: 'ok',
    last_error: '',
    next_run: '',
    running: false,
    queued: true,
    ...extra,
  };
}

function job(extra: Partial<Job> = {}): Job {
  return {
    id: 1,
    kind: 'search_episode',
    payload: '',
    state: 'running',
    attempts: 1,
    run_after: '',
    lease_expires_at: '',
    last_error: '',
    created_at: '',
    updated_at: '',
    ...extra,
  };
}

let host: HTMLElement;
let app: Record<string, unknown>;
let taskRows: SystemTask[];
let jobRows: Job[];

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  window.scrollTo = () => {};
  seedStatus();
  taskRows = [];
  jobRows = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/downloads')) return jsonResponse({ downloads: [] });
      if (url.endsWith('/requests')) return jsonResponse({ requests: [] });
      if (url.includes('/system/tasks')) return jsonResponse({ tasks: taskRows });
      if (url.includes('/jobs')) return jsonResponse({ jobs: jobRows });
      if (url.endsWith('/system/status')) return jsonResponse(system.status ?? {});
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
});

afterEach(async () => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
  localStorage.clear();
  document.documentElement.removeAttribute('data-theme');
  system.status = null;
  system.loading = true;
  tasks.stopSoon();
  tasks.tasks = null;
  tasks.jobs = null;
  session.forget();
});

async function settle() {
  for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

async function render(props: { settingsSection?: string } = {}) {
  app = mount(Sidebar, { target: host, props: { open: true, onclose: () => {}, ...props } });
  await settle();
}

function activity(): HTMLAnchorElement[] {
  return [...host.querySelectorAll<HTMLAnchorElement>('[data-sidebar-activity-row]')];
}

describe('Sidebar task rail', () => {
  it('stays quiet while nothing is running and nothing failed', async () => {
    await render();

    expect(activity()).toEqual([]);
    expect(host.querySelector('[data-sidebar-footer]')).not.toBeNull();
    expect(host.textContent).not.toContain('Ready');
    expect(host.textContent).not.toContain('500 GB free');
    expect(host.textContent).not.toContain('No TMDB key');
    expect(host.querySelector('[role="progressbar"]')).toBeNull();
    expect(host.querySelector('a[href="/settings"] > span[title]')).toBeNull();
  });

  it('stacks two named searches at once', async () => {
    jobRows = [
      job({
        id: 1,
        subject: 'Transfixed',
        subject_kind: 'site',
        subject_id: 9,
      }),
      job({
        id: 2,
        subject: 'Transfixed',
        subject_kind: 'site',
        subject_id: 9,
      }),
      job({
        id: 3,
        subject: 'Severance',
        subject_kind: 'series',
        subject_id: 3,
      }),
    ];
    await render();

    expect(activity().map((row) => ({
      text: row.textContent?.replace(/\s+/g, ' ').trim(),
      href: row.getAttribute('href'),
    }))).toEqual([
      { text: 'Searching 2 scenes from Transfixed', href: '/adult/sites/9' },
      { text: 'Searching Severance', href: '/series/3' },
    ]);
  });

  it('shows a live search and sends the click to the work it names', async () => {
    jobRows = [job()];
    await render();

    expect(activity()).toHaveLength(1);
    expect(activity()[0]?.textContent).toContain('Searching');
    expect(activity()[0]?.getAttribute('href')).toBe('/wanted');
    expect(activity()[0]?.querySelector('.sidebar-task-pulse')).not.toBeNull();
    expect(activity()[0]?.className).toContain('text-accent-text');
  });

  it('warns about a failed last run without a pulse', async () => {
    taskRows = [task({ last_result: 'failed', last_error: 'indexer timed out' })];
    await render();

    expect(activity()[0]?.textContent).toContain('RSS sync failed');
    expect(activity()[0]?.getAttribute('title')).toContain('indexer timed out');
    expect(activity()[0]?.querySelector('.sidebar-task-pulse')).toBeNull();
    expect(activity()[0]?.className).toContain('text-warning');
  });

  it('badges Settings when a recurring task failed', async () => {
    taskRows = [task({ last_result: 'failed' })];
    await render();

    const badge = host.querySelector('a[href="/settings"] > span[title="1 task needs attention"]');
    expect(badge).not.toBeNull();
    expect(badge?.classList).toContain('bg-warning-tint');
    expect(badge?.textContent).toContain('1');
  });

  it('badges the Tasks row once the settings rail is open', async () => {
    taskRows = [task({ last_result: 'failed' })];
    await render({ settingsSection: '' });

    const badge = host.querySelector(
      'a[href="/settings/tasks#tasks"] span[title="1 task needs attention"]',
    );
    expect(badge).not.toBeNull();
    expect(host.querySelector('[data-settings-sidebar-navigation]')).not.toBeNull();
  });

  it('keeps the rail off a member account', async () => {
    session.user = { username: 'ada', role: 'member', open: false, adult: false };
    jobRows = [job()];
    taskRows = [task({ last_result: 'failed' })];
    await render();

    expect(activity()).toEqual([]);
    expect(host.querySelector('a[href="/settings"]')).toBeNull();
    expect(host.textContent).toContain('Sign out ada');
  });

  it('keeps sign-out when an administrator is named', async () => {
    session.user = { username: 'root', role: 'admin', open: false, adult: false };
    await render();

    expect(host.textContent).toContain('Sign out root');
    expect(host.querySelector('[data-sidebar-footer]')).not.toBeNull();
  });

  it('puts a theme toggle to the right of sign-out and flips the document theme', async () => {
    saveDisplayPreferences({ theme: 'dark', motion: 'system' });
    session.user = { username: 'ada', role: 'admin', open: false, adult: false };
    await render();

    const toggle = host.querySelector<HTMLButtonElement>('[data-sidebar-theme]');
    expect(toggle).not.toBeNull();
    expect(toggle?.getAttribute('aria-label')).toBe('Use the light theme');

    toggle?.click();
    await settle();

    expect(document.documentElement.dataset.theme).toBe('light');
    expect(host.querySelector('[data-sidebar-theme]')?.getAttribute('aria-label')).toBe(
      'Use the dark theme',
    );
  });
});
