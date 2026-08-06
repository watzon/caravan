/**
 * The series grid shares select mode with the movie grid (Movies.test.ts
 * covers the behaviour); what is series-specific is the endpoints it calls and
 * that "series" is its own plural.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Series from './Series.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';
import { navigate, router } from '../router.svelte';

function series(
  id: number,
  title: string,
  options: {
    addedAt?: string;
    state?: 'downloaded' | 'incomplete' | 'wanted' | 'unmonitored';
  } = {},
) {
  const state = options.state ?? 'downloaded';
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
    monitored: state !== 'unmonitored',
    quality_profile_id: 0,
    first_aired: '',
    added_at: options.addedAt ?? '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    episode_count: 9,
    episode_file_count: state === 'downloaded' ? 9 : state === 'incomplete' ? 4 : 0,
  };
}

const SERIES = [
  series(2, 'Severance', { addedAt: '2026-02-01T00:00:00Z' }),
  series(1, 'Andor', { addedAt: '2026-01-01T00:00:00Z', state: 'unmonitored' }),
];
let servedSeries = SERIES;

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: { url: string; method: string }[];

beforeEach(() => {
  clearToasts();
  servedSeries = SERIES;
  window.scrollTo = () => {};
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
      return new Response(JSON.stringify({ series: servedSeries }), {
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

async function open(url = '/series') {
  window.history.replaceState({}, '', url);
  navigate(url, { replace: true });
  app = mount(Series, { target: host, props: { onadd: () => {} } });
  await settle();
}

function button(label: string): HTMLButtonElement {
  const found = [...host.querySelectorAll('button')].find(
    (b) => b.textContent?.trim() === label,
  );
  expect(found, `${label} button`).toBeTruthy();
  return found as HTMLButtonElement;
}

function filterChip(label: string): HTMLButtonElement {
  const filterButtons = host.querySelectorAll<HTMLButtonElement>(
    '[aria-label="Filter library"] button',
  );
  const found = [...filterButtons].find((candidate) =>
    [...candidate.querySelectorAll('span')].some(
      (span) => span.textContent?.trim() === label,
    ),
  );
  expect(found, `${label} filter`).toBeTruthy();
  return found!;
}

function cards(): HTMLElement[] {
  return [...host.querySelectorAll<HTMLElement>('button[aria-pressed][aria-label]')];
}

/** The per-card check circle that starts a selection. */
async function select(title: string) {
  const circle = host.querySelector<HTMLButtonElement>(
    `button[aria-label^="Select ${title} (2022), "]`,
  );
  expect(circle, `the select circle on ${title}`).toBeTruthy();
  circle!.click();
  await settle();
}

function sortTrigger(): HTMLButtonElement {
  const trigger = [
    ...host.querySelectorAll<HTMLButtonElement>('button[aria-haspopup="dialog"]'),
  ].find((button) => (button.textContent ?? '').trim().startsWith('Sort'));
  expect(trigger, 'series sort dropdown').toBeTruthy();
  return trigger!;
}

/** The trigger's accessible name is "Sort: {current}", the value it sorts by. */
function sortValue(): string {
  return sortTrigger().textContent?.trim().replace(/^Sort:\s*/, '') ?? '';
}

async function chooseSort(name: 'Title' | 'Added' | 'Status') {
  if (!host.querySelector('[role="dialog"][aria-label^="Sort"]')) {
    sortTrigger().click();
    await settle();
  }
  const panel = host.querySelector<HTMLElement>('[role="dialog"][aria-label^="Sort"]');
  const option = [...(panel?.querySelectorAll<HTMLButtonElement>('button') ?? [])].find(
    (button) => (button.textContent ?? '').trim() === name,
  );
  expect(option, `sort option "${name}"`).toBeTruthy();
  option!.click();
  await settle();
  // A pick leaves the popover open for a second one; dismiss it as a reader does.
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
  await settle();
}

function cardIDs(): number[] {
  return [...host.querySelectorAll<HTMLAnchorElement>('a[href^="/series/"]')].map((link) =>
    Number(link.pathname.split('/').pop()),
  );
}

describe('Series grid selection', () => {
  it('keeps cards as links while nothing is selected', async () => {
    await open();

    expect(host.querySelector('a[href="/series/2"]')).toBeTruthy();
    expect(cards()).toHaveLength(0);
    expect(host.querySelectorAll('button[aria-label^="Select "]')).toHaveLength(2);
  });

  it('labels the sort and title filter controls', async () => {
    await open();

    expect(sortValue()).toBe('Title');
    expect(
      host.querySelector<HTMLInputElement>('input[type="search"]')?.getAttribute('aria-label'),
    ).toBe('Filter series by title');
  });

  it('unmonitors the selection through the series endpoints', async () => {
    await open();

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
    await open();

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

  it('derives added sort from the URL on reload', async () => {
    await open('/series?sort=added&layout=posters');

    expect(sortValue()).toBe('Added');
    expect(cardIDs()).toEqual([2, 1]);
    expect(router.params.get('layout')).toBe('posters');
  });

  it('keeps invalid sort in the URL while falling back to stable title and id order', async () => {
    servedSeries = [series(8, 'Zulu'), series(6, 'Alpha'), series(3, 'Alpha')];
    await open('/series?sort=oldest&layout=compact');

    expect(sortValue()).toBe('Title');
    expect(cardIDs()).toEqual([3, 6, 8]);
    expect(router.params.get('sort')).toBe('oldest');
    expect(router.params.get('layout')).toBe('compact');
  });

  it('preserves filters, selection, and unrelated query keys across sort changes', async () => {
    await open('/series?layout=compact');
    filterChip('Unmonitored').click();
    await settle();
    expect(cardIDs()).toEqual([1]);

    await select('Andor');
    await chooseSort('Status');

    expect(router.path).toBe('/series');
    expect(router.params.get('sort')).toBe('status');
    expect(router.params.get('layout')).toBe('compact');
    expect(filterChip('Unmonitored').getAttribute('aria-pressed')).toBe('true');
    expect(cards().map((card) => card.getAttribute('aria-label'))).toEqual([
      'Andor (2022), Unmonitored',
    ]);
    expect(cards()[0]?.getAttribute('aria-pressed')).toBe('true');

    await chooseSort('Title');
    expect(router.params.has('sort')).toBe(false);
    expect(router.params.get('layout')).toBe('compact');
    expect(filterChip('Unmonitored').getAttribute('aria-pressed')).toBe('true');
    expect(cards()[0]?.getAttribute('aria-pressed')).toBe('true');
  });
});
