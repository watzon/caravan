/**
 * Selection on the movie grid. The regression that matters most is the
 * default: while nothing is selected a poster card is still a plain link,
 * because that is how the whole SPA navigates. Selection starts on the card
 * itself — the check circle over the poster — and the floating action bar
 * exists only while the selection holds something.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Movies from './Movies.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';
import { navigate, router } from '../router.svelte';

function movie(
  id: number,
  title: string,
  options: { addedAt?: string; monitored?: boolean; downloaded?: boolean } = {},
) {
  return {
    id,
    tmdb_id: id,
    imdb_id: '',
    title,
    sort_title: title.toLowerCase(),
    year: 2021,
    overview: '',
    path: '',
    poster_path: '',
    poster_url: '',
    monitored: options.monitored ?? true,
    quality_profile_id: 0,
    release_date: '',
    added_at: options.addedAt ?? '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    file: options.downloaded ? { id } : null,
  };
}

const MOVIES = [
  movie(3, 'Sicario', { addedAt: '2026-03-01T00:00:00Z' }),
  movie(2, 'Dune', { addedAt: '2026-02-01T00:00:00Z', monitored: false }),
  movie(1, 'Arrival', { addedAt: '2026-01-01T00:00:00Z', downloaded: true }),
];
let servedMovies = MOVIES;

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: { url: string; method: string }[];

function stubFetch() {
  calls = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      calls.push({ url, method });
      if (method === 'DELETE') return new Response(null, { status: 204 });
      if (method === 'PATCH') {
        return new Response(JSON.stringify(movie(1, 'Arrival')), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (method === 'POST') {
        return new Response(JSON.stringify({ queued: 1 }), {
          status: 202,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return new Response(JSON.stringify({ movies: servedMovies }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
}

beforeEach(() => {
  clearToasts();
  servedMovies = MOVIES;
  window.scrollTo = () => {};
  stubFetch();
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

async function open(url = '/movies') {
  window.history.replaceState({}, '', url);
  navigate(url, { replace: true });
  app = mount(Movies, { target: host, props: { onadd: () => {} } });
  await settle();
}

function button(label: string): HTMLButtonElement {
  const found = [...host.querySelectorAll('button')].find(
    (b) => b.textContent?.trim() === label,
  );
  expect(found, `${label} button`).toBeTruthy();
  return found as HTMLButtonElement;
}

/** The per-card check circle that starts a selection. */
async function select(title: string) {
  const circle = host.querySelector<HTMLButtonElement>(
    `button[aria-label^="Select ${title} (2021), "]`,
  );
  expect(circle, `the select circle on ${title}`).toBeTruthy();
  circle!.click();
  await settle();
}

/** The cards while a selection is active: whole-card toggle buttons. */
function cards(): HTMLElement[] {
  return [...host.querySelectorAll<HTMLElement>('button[aria-pressed][aria-label]')].filter(
    (el) => el.getAttribute('aria-label')?.includes('('),
  );
}

function methodsOf(method: string): string[] {
  return calls.filter((c) => c.method === method).map((c) => c.url);
}

function sortTrigger(): HTMLButtonElement {
  const trigger = [
    ...host.querySelectorAll<HTMLButtonElement>('button[aria-haspopup="dialog"]'),
  ].find((button) => (button.textContent ?? '').trim().startsWith('Sort'));
  expect(trigger, 'movie sort dropdown').toBeTruthy();
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
  return [...host.querySelectorAll<HTMLAnchorElement>('a[href^="/movies/"]')].map((link) =>
    Number(link.pathname.split('/').pop()),
  );
}

describe('Movies grid', () => {
  it('renders poster cards as plain links with a select circle each', async () => {
    await open();

    const link = host.querySelector('a[href="/movies/2"]');
    expect(link, 'a card is a link while nothing is selected').toBeTruthy();
    expect(link?.getAttribute('aria-label')).toBe('Dune (2021), Unmonitored');
    expect(host.querySelectorAll('button[aria-label^="Select "]')).toHaveLength(3);
    // Filter chips carry aria-pressed too; a card toggle also carries the
    // title and status as its accessible name.
    expect(host.querySelector('button[aria-pressed][aria-label]')).toBeNull();
  });

  it('labels the sort and title filter and keeps card status textual', async () => {
    await open();

    expect(sortValue()).toBe('Added');
    expect(
      host.querySelector<HTMLInputElement>('input[type="search"]')?.getAttribute('aria-label'),
    ).toBe('Filter movies by title');
    const downloadedCard = host.querySelector('a[href="/movies/1"]');
    const downloadedStatus = [...(downloadedCard?.querySelectorAll('span') ?? [])].find(
      (element) => element.textContent?.trim() === 'Downloaded',
    );
    expect(downloadedStatus, 'visible status on the downloaded card').toBeTruthy();
    expect(downloadedStatus?.classList.contains('sr-only')).toBe(false);
  });

  it('starts a selection from a card circle and ends it by deselecting the last card', async () => {
    await open();

    await select('Dune');
    expect(host.textContent).toContain('1 selected');
    expect(host.querySelector('a[href="/movies/2"]'), 'cards stop being links').toBeNull();
    expect(cards()).toHaveLength(3);
    expect(cards()[1]!.getAttribute('aria-pressed')).toBe('true');

    // Toggling the last selected card off dismisses the bar and the toggles.
    cards()[1]!.click();
    await settle();
    expect(host.textContent).not.toContain('selected');
    expect(host.querySelector('a[href="/movies/2"]'), 'cards are links again').toBeTruthy();
  });

  it('monitors every selected movie and summarizes the result', async () => {
    await open();
    await select('Arrival');
    cards().find((card) => card.getAttribute('aria-label')?.startsWith('Sicario '))!.click();
    await settle();
    expect(host.textContent).toContain('2 selected');

    button('Monitor').click();
    await settle();

    expect(methodsOf('PATCH')).toEqual(['/api/v1/library/movies/1', '/api/v1/library/movies/3']);
    expect(toasts.items.map((t) => t.message)).toEqual(['Monitored 2']);
  });

  it('queues a search for every selected movie', async () => {
    await open();
    await select('Arrival');

    button('Search').click();
    await settle();

    expect(methodsOf('POST')).toEqual(['/api/v1/library/movies/1/search']);
    expect(toasts.items.map((t) => t.message)).toEqual(['Queued searches for 1']);
  });

  it('removes the selection behind one confirm, then reloads and drops the selection', async () => {
    await open();
    await select('Arrival');
    cards()[1]!.click();
    await settle();

    button('Remove…').click();
    await settle();
    expect(methodsOf('DELETE'), 'opening the confirm deletes nothing').toEqual([]);

    button('Remove').click();
    await settle();

    expect(methodsOf('DELETE')).toEqual([
      '/api/v1/library/movies/1',
      '/api/v1/library/movies/2',
    ]);
    expect(toasts.items.map((t) => t.message)).toEqual(['Removed 2']);
    // The list is re-read and the grid is a set of links again.
    expect(calls.filter((call) => call.method === 'GET' && call.url.endsWith('/library/movies'))).toHaveLength(2);
    expect(host.querySelector('a[href="/movies/2"]')).toBeTruthy();
  });

  it('deletes files too when the confirm checkbox is ticked', async () => {
    await open();
    await select('Arrival');

    button('Remove…').click();
    await settle();

    const checkbox = host.querySelector<HTMLInputElement>('input[type="checkbox"]');
    expect(checkbox, 'delete-files checkbox').toBeTruthy();
    checkbox!.checked = true;
    checkbox!.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();

    button('Remove').click();
    await settle();

    expect(methodsOf('DELETE')).toEqual(['/api/v1/library/movies/1?files=true']);
  });

  it('sends nothing when the remove confirm is cancelled', async () => {
    await open();
    await select('Arrival');

    button('Remove…').click();
    await settle();
    // Scoped to the dialog: only its Cancel exists, but scoping keeps it honest.
    const cancel = [...host.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find(
      (b) => b.textContent?.trim() === 'Cancel',
    );
    expect(cancel, 'the confirm Cancel button').toBeTruthy();
    cancel!.click();
    await settle();

    expect(methodsOf('DELETE')).toEqual([]);
    expect(host.querySelector('[role="dialog"]')).toBeNull();
    // Cancelling the confirm is not cancelling the selection.
    expect(host.textContent).toContain('1 selected');
  });

  it('drops the selection on Escape and on the bar’s clear button', async () => {
    await open();
    await select('Arrival');
    expect(host.textContent).toContain('1 selected');

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await settle();
    expect(host.querySelector('a[href="/movies/1"]')).toBeTruthy();

    // A fresh selection starts from the card, empty of the old one.
    await select('Dune');
    expect(host.textContent).toContain('1 selected');
    button('Clear selection').click();
    await settle();
    expect(host.textContent).not.toContain('selected');
    expect(host.querySelector('a[href="/movies/2"]')).toBeTruthy();
  });
  it('uses added time as the default sort and keeps unrelated URL state', async () => {
    await open('/movies?layout=posters');

    expect(sortValue()).toBe('Added');
    expect(cardIDs()).toEqual([3, 2, 1]);
    expect(router.params.get('layout')).toBe('posters');
  });

  it('falls back to added order without rewriting an invalid URL', async () => {
    servedMovies = [movie(9, 'Zulu'), movie(7, 'Alpha'), movie(4, 'Alpha')];
    await open('/movies?sort=sideways&layout=compact');

    expect(sortValue()).toBe('Added');
    expect(cardIDs()).toEqual([4, 7, 9]);
    expect(router.params.get('sort')).toBe('sideways');
    expect(router.params.get('layout')).toBe('compact');
  });

  it('writes non-default sort and removes the default while preserving other query state', async () => {
    await open('/movies?layout=compact');

    await chooseSort('Status');
    expect(router.path).toBe('/movies');
    expect(router.params.get('sort')).toBe('status');
    expect(router.params.get('layout')).toBe('compact');
    expect(cardIDs()).toEqual([1, 3, 2]);

    await chooseSort('Added');
    expect(router.path).toBe('/movies');
    expect(router.params.has('sort')).toBe(false);
    expect(router.params.get('layout')).toBe('compact');
    expect(cardIDs()).toEqual([3, 2, 1]);
  });

  it('filters before sorting and preserves the filter and selection across sort changes', async () => {
    await open('/movies?layout=posters');
    const input = host.querySelector<HTMLInputElement>('input[aria-label="Filter movies by title"]');
    expect(input).toBeTruthy();
    input!.value = 'i';
    input!.dispatchEvent(new Event('input', { bubbles: true }));
    await settle();
    expect(cardIDs()).toEqual([3, 1]);

    await select('Arrival');
    await chooseSort('Title');

    expect(input!.value).toBe('i');
    expect(router.params.get('layout')).toBe('posters');
    expect(router.params.get('sort')).toBe('title');
    expect(cards().map((card) => card.getAttribute('aria-label'))).toEqual([
      'Arrival (2021), Downloaded',
      'Sicario (2021), Wanted',
    ]);
    expect(
      cards()
        .find((card) => card.getAttribute('aria-label') === 'Arrival (2021), Downloaded')
        ?.getAttribute('aria-pressed'),
    ).toBe('true');
  });
});
