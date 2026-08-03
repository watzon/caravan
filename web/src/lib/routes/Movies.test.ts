/**
 * Select mode on the movie grid. The regression that matters most is the
 * default: without select mode a poster card is still a plain link, because
 * that is how the whole SPA navigates.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Movies from './Movies.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';

function movie(id: number, title: string) {
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
    monitored: true,
    quality_profile_id: 0,
    release_date: '',
    added_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    file: null,
  };
}

const MOVIES = [movie(1, 'Arrival'), movie(2, 'Dune'), movie(3, 'Sicario')];

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
      return new Response(JSON.stringify({ movies: MOVIES }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
}

beforeEach(() => {
  clearToasts();
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

async function open() {
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

/** The cards, whichever element they are rendered as. */
function cards(): HTMLElement[] {
  return [...host.querySelectorAll<HTMLElement>('a[aria-label], button[aria-pressed]')].filter(
    (el) => el.getAttribute('aria-label')?.includes('('),
  );
}

function methodsOf(method: string): string[] {
  return calls.filter((c) => c.method === method).map((c) => c.url);
}

describe('Movies grid', () => {
  it('renders poster cards as plain links outside select mode', async () => {
    await open();

    const link = host.querySelector('a[href="/movies/2"]');
    expect(link, 'a card is a link when nothing is being selected').toBeTruthy();
    expect(link?.getAttribute('aria-label')).toBe('Dune (2021)');
    // Filter chips carry aria-pressed too; a card toggle also carries the
    // title as its accessible name.
    expect(host.querySelector('button[aria-pressed][aria-label]')).toBeNull();
  });

  it('turns cards into toggles while selecting and back again on Done', async () => {
    await open();

    button('Select').click();
    await settle();
    expect(host.querySelector('a[href="/movies/2"]'), 'cards stop being links').toBeNull();
    expect(cards()).toHaveLength(3);

    cards()[1]!.click();
    await settle();
    expect(cards()[1]!.getAttribute('aria-pressed')).toBe('true');
    expect(host.textContent).toContain('1 selected');

    // Toggling the same card takes it back out.
    cards()[1]!.click();
    await settle();
    expect(cards()[1]!.getAttribute('aria-pressed')).toBe('false');
    expect(host.textContent).toContain('0 selected');

    button('Done').click();
    await settle();
    expect(host.querySelector('a[href="/movies/2"]'), 'cards are links again').toBeTruthy();
  });

  it('monitors every selected movie and summarizes the result', async () => {
    await open();
    button('Select').click();
    await settle();

    cards()[0]!.click();
    cards()[2]!.click();
    await settle();

    button('Monitor').click();
    await settle();

    expect(methodsOf('PATCH')).toEqual(['/api/v1/library/movies/1', '/api/v1/library/movies/3']);
    expect(toasts.items.map((t) => t.message)).toEqual(['Monitored 2']);
  });

  it('queues a search for every selected movie', async () => {
    await open();
    button('Select').click();
    await settle();

    cards()[0]!.click();
    await settle();

    button('Search').click();
    await settle();

    expect(methodsOf('POST')).toEqual(['/api/v1/library/movies/1/search']);
    expect(toasts.items.map((t) => t.message)).toEqual(['Queued searches for 1']);
  });

  it('removes the selection behind one confirm, then reloads and leaves select mode', async () => {
    await open();
    button('Select').click();
    await settle();

    cards()[0]!.click();
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
    expect(methodsOf('GET')).toHaveLength(2);
    expect(host.querySelector('a[href="/movies/2"]')).toBeTruthy();
  });

  it('deletes files too when the confirm checkbox is ticked', async () => {
    await open();
    button('Select').click();
    await settle();

    cards()[0]!.click();
    await settle();

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
    button('Select').click();
    await settle();

    cards()[0]!.click();
    await settle();

    button('Remove…').click();
    await settle();
    // Scoped to the dialog: the action bar has its own Done beside it.
    const cancel = [...host.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find(
      (b) => b.textContent?.trim() === 'Cancel',
    );
    expect(cancel, 'the confirm Cancel button').toBeTruthy();
    cancel!.click();
    await settle();

    expect(methodsOf('DELETE')).toEqual([]);
    expect(host.querySelector('[role="dialog"]')).toBeNull();
    // Cancelling the confirm is not cancelling select mode.
    expect(host.textContent).toContain('1 selected');
  });

  it('leaves select mode on Escape, and clears the selection with it', async () => {
    await open();
    button('Select').click();
    await settle();

    cards()[0]!.click();
    await settle();
    expect(host.textContent).toContain('1 selected');

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await settle();
    expect(host.querySelector('a[href="/movies/1"]')).toBeTruthy();

    // Re-entering starts empty rather than resurrecting the old selection.
    button('Select').click();
    await settle();
    expect(host.textContent).toContain('0 selected');
  });
});
