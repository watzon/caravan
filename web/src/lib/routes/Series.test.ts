/**
 * The series grid shares select mode with the movie grid (Movies.test.ts
 * covers the behaviour); what is series-specific is the endpoints it calls and
 * that "series" is its own plural.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Series from './Series.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';

function series(id: number, title: string) {
  return {
    id,
    tmdb_id: id,
    tvdb_id: 0,
    imdb_id: '',
    title,
    sort_title: title.toLowerCase(),
    year: 2022,
    overview: '',
    status: 'Continuing',
    path: '',
    poster_path: '',
    poster_url: '',
    monitored: true,
    quality_profile_id: 0,
    first_aired: '',
    added_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    episode_count: 9,
    episode_file_count: 9,
  };
}

const SERIES = [series(1, 'Andor'), series(2, 'Severance')];

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: { url: string; method: string }[];

beforeEach(() => {
  clearToasts();
  calls = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      calls.push({ url, method });
      if (method === 'DELETE') return new Response(null, { status: 204 });
      if (method === 'PATCH') {
        return new Response(JSON.stringify(series(1, 'Andor')), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return new Response(JSON.stringify({ series: SERIES }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  clearToasts();
  vi.unstubAllGlobals();
});

async function settle() {
  await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

function button(label: string): HTMLButtonElement {
  const found = [...host.querySelectorAll('button')].find(
    (b) => b.textContent?.trim() === label,
  );
  expect(found, `${label} button`).toBeTruthy();
  return found as HTMLButtonElement;
}

function cards(): HTMLElement[] {
  return [...host.querySelectorAll<HTMLElement>('button[aria-pressed][aria-label]')];
}

/** The per-card check circle that starts a selection. */
async function select(title: string) {
  const circle = host.querySelector<HTMLButtonElement>(
    `button[aria-label="Select ${title} (2022)"]`,
  );
  expect(circle, `the select circle on ${title}`).toBeTruthy();
  circle!.click();
  await settle();
}

describe('Series grid selection', () => {
  it('keeps cards as links while nothing is selected', async () => {
    app = mount(Series, { target: host, props: { onadd: () => {} } });
    await settle();

    expect(host.querySelector('a[href="/series/2"]')).toBeTruthy();
    expect(cards()).toHaveLength(0);
    expect(host.querySelectorAll('button[aria-label^="Select "]')).toHaveLength(2);
  });

  it('unmonitors the selection through the series endpoints', async () => {
    app = mount(Series, { target: host, props: { onadd: () => {} } });
    await settle();

    await select('Andor');
    cards()[1]!.click();
    await settle();

    button('Unmonitor').click();
    await settle();

    expect(calls.filter((c) => c.method === 'PATCH').map((c) => c.url)).toEqual([
      '/api/v1/library/series/1',
      '/api/v1/library/series/2',
    ]);
    expect(toasts.items.map((t) => t.message)).toEqual(['Unmonitored 2']);
  });

  it('removes the selection and does not pluralize "series"', async () => {
    app = mount(Series, { target: host, props: { onadd: () => {} } });
    await settle();

    await select('Severance');

    button('Remove…').click();
    await settle();
    expect(host.querySelector('[role="dialog"]')?.textContent).toContain('1 selected series');

    button('Remove').click();
    await settle();

    expect(calls.filter((c) => c.method === 'DELETE').map((c) => c.url)).toEqual([
      '/api/v1/library/series/2',
    ]);
    expect(toasts.items.map((t) => t.message)).toEqual(['Removed 1']);
  });
});
