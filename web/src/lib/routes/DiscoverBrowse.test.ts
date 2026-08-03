/**
 * The paged shelf. What is asserted here is the paging arithmetic the pure
 * helpers cannot cover, because the failure is a keyed `{#each}` blowing up:
 * a retry must not re-append the page that already worked, and a page handed
 * back twice must not produce two rows with the same key.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import DiscoverBrowse from './DiscoverBrowse.svelte';
import type { DiscoverItem } from '../api/types';

function item(tmdbID: number): DiscoverItem {
  return {
    media_type: 'series',
    tmdb_id: tmdbID,
    title: `Title ${tmdbID}`,
    year: 2020,
    overview: '',
    poster_path: '',
    poster_url: '',
    backdrop_url: '',
    vote_average: 7,
    date: '2020-01-01',
    in_library: false,
    library_id: 0,
    requested: false,
  };
}

/** One page of the stub shelf: two rows whose ids follow the page number. */
function pageBody(page: number, totalPages = 5) {
  return {
    source: { id: 213, name: 'Netflix', type: 'network' },
    page,
    total_pages: totalPages,
    items: [item(page * 10), item(page * 10 + 1)],
  };
}

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let requested: number[];
/** Pages queued to fail once, by page number. */
let failOnce: Set<number>;
/** What the server answers with, whatever was asked for. */
let served: (page: number) => ReturnType<typeof pageBody>;

function stubFetch() {
  requested = [];
  failOnce = new Set();
  served = (page) => pageBody(page);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const page = Number(new URL(String(input), 'http://x').searchParams.get('page') ?? '1');
      requested.push(page);
      if (failOnce.has(page)) {
        failOnce.delete(page);
        return new Response(JSON.stringify({ error: 'upstream said no' }), {
          status: 502,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return new Response(JSON.stringify(served(page)), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
}

async function settle() {
  for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

function clickText(text: string) {
  const button = [...host.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.textContent?.trim() === text,
  );
  expect(button, `a "${text}" button`).toBeTruthy();
  button!.click();
  flushSync();
}

function cardTitles(): string[] {
  return [...host.querySelectorAll('a[href^="/discover/"]')]
    .map((a) => a.getAttribute('href') ?? '')
    .filter((href) => href !== '/discover');
}

beforeEach(() => {
  stubFetch();
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  vi.unstubAllGlobals();
});

async function mountBrowse() {
  app = mount(DiscoverBrowse, {
    target: host,
    props: { type: 'network' as const, id: 213 },
  }) as Record<string, unknown>;
  flushSync();
  await settle();
}

describe('DiscoverBrowse — paging', () => {
  it('retries the page that failed, not the one that already loaded', async () => {
    await mountBrowse();
    clickText('Load more');
    await settle();
    expect(requested).toEqual([1, 2]);

    // Page 3 blips. `page` only advances on success, so it is still 2.
    failOnce.add(3);
    clickText('Load more');
    await settle();
    expect(requested).toEqual([1, 2, 3]);
    expect(host.textContent).toContain('upstream said no');

    clickText('Retry');
    await settle();
    // The retry asks for 3 again — asking for 2 would append it a second time.
    expect(requested).toEqual([1, 2, 3, 3]);

    const hrefs = cardTitles();
    expect(new Set(hrefs).size).toBe(hrefs.length);
    expect(hrefs).toHaveLength(6);
  });

  it('drops rows a page repeats rather than keying two cards the same', async () => {
    await mountBrowse();
    // TMDB at its page ceiling: asking past the last page serves it again.
    served = () => pageBody(1);

    clickText('Load more');
    await settle();

    const hrefs = cardTitles();
    expect(hrefs).toEqual(['/discover/series/10', '/discover/series/11']);
  });
});
