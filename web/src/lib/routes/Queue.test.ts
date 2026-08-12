/**
 * Queue filtering: the default view hides finished work — completed imports
 * and torrents that finished downloading and sit paused — while Done and All
 * stay one click away.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import type { DownloadStatus } from '../api/types';
import Queue from './Queue.svelte';
import { downloads } from '../state/downloads.svelte';
import { toasts } from '../state/toast.svelte';

function download(overrides: Partial<DownloadStatus>): DownloadStatus {
  return {
    id: 'id-' + String(overrides.name ?? Math.random()),
    state: 'downloading',
    name: 'x',
    progress: 0.5,
    bytes_done: 512,
    size: 1024,
    down_rate: 0,
    up_rate: 0,
    eta_seconds: 0,
    ratio: 0,
    save_path: 'incomplete/x',
    error: '',
    max_down_rate: 0,
    max_up_rate: 0,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

const QUEUE: DownloadStatus[] = [
  download({
    name: 'still-downloading',
    state: 'downloading',
    progress: 0.4,
    created_at: '2026-01-02T00:00:00Z',
  }),
  download({
    name: 'paused-mid-download',
    state: 'paused',
    progress: 0.6,
    created_at: '2026-01-05T00:00:00Z',
  }),
  download({
    name: 'seeding-away',
    state: 'seeding',
    progress: 1,
    created_at: '2026-01-03T00:00:00Z',
  }),
  download({
    name: 'imported-and-done',
    state: 'completed',
    progress: 1,
    created_at: '2026-01-01T00:00:00Z',
  }),
  download({
    name: 'finished-parked-torrent',
    state: 'paused',
    progress: 1,
    created_at: '2026-01-04T00:00:00Z',
  }),
];

let host: HTMLElement;
let app: Record<string, unknown>;
/** What the stubbed queue endpoint answers next, so a test can move it. */
let queue: DownloadStatus[] = QUEUE;

beforeEach(() => {
  queue = QUEUE;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/insight')) {
        return new Response(
          JSON.stringify({ insight: { peers: [], trackers: [], availability: 0 } }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        );
      }
      if (url.includes('/downloads')) {
        return new Response(JSON.stringify({ downloads: queue }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
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
});

function rowNames(): string[] {
  // The name is the flex-1 span carrying the full name as its title; badges
  // and metric spans carry titles too, so the class is part of the selector.
  return [...host.querySelectorAll('li span.flex-1[title]')].map(
    (el) => el.textContent?.trim() ?? '',
  );
}

async function mountQueue() {
  app = mount(Queue, { target: host }) as Record<string, unknown>;
  flushSync();
  // The store's initial refresh crosses the stubbed fetch and Response.json;
  // drain event-loop turns until rows land.
  for (let i = 0; i < 20 && rowNames().length === 0; i++) {
    await new Promise((resolve) => setTimeout(resolve, 0));
    flushSync();
  }
  // Let the subscribe-time fetch finish even when rows were already on the
  // shared store: the store drops an overlapping refresh, so a test that moves
  // the payload and re-polls immediately would otherwise be answered by the
  // request already in flight with the old one.
  for (let i = 0; i < 5; i++) {
    await new Promise((resolve) => setTimeout(resolve, 0));
    flushSync();
  }
}

function pill(label: string): HTMLButtonElement {
  const found = [...host.querySelectorAll('button')].find((b) =>
    b.textContent?.trim().startsWith(label),
  );
  expect(found, `pill ${label}`).toBeDefined();
  return found!;
}

describe('Queue filtering', () => {
  it('hides finished items by default, including paused finished torrents', async () => {
    await mountQueue();
    expect(rowNames()).toEqual(['paused-mid-download', 'seeding-away', 'still-downloading']);
  });

  it('shows the finished bucket under Done', async () => {
    await mountQueue();
    pill('Done').click();
    flushSync();
    expect(rowNames()).toEqual(['finished-parked-torrent', 'imported-and-done']);
  });

  it('replaces dead rate and ETA metrics on finished rows', async () => {
    await mountQueue();
    pill('Done').click();
    flushSync();

    for (const row of host.querySelectorAll('li')) {
      expect(row.textContent).toContain('Download complete');
      expect(row.textContent).not.toContain('ETA');
      expect(row.textContent).not.toContain('↓');
      expect(row.textContent).not.toContain('↑');
    }
  });

  it('shows everything under All', async () => {
    await mountQueue();
    pill('All').click();
    flushSync();
    expect(rowNames()).toEqual([
      'paused-mid-download',
      'finished-parked-torrent',
      'seeding-away',
      'still-downloading',
      'imported-and-done',
    ]);
  });

  it('labels the delete-data option and preserves the full name in the removal dialog', async () => {
    await mountQueue();
    const remove = [...host.querySelectorAll<HTMLButtonElement>('button')].find((button) =>
      button.textContent?.includes('Remove still-downloading'),
    );
    expect(remove).toBeDefined();
    remove!.click();
    flushSync();

    const deleteData = host.querySelector<HTMLInputElement>('input[type="checkbox"]');
    expect(deleteData?.closest('label')?.textContent).toContain('Also delete the downloaded data');
    expect(host.querySelector('[role="dialog"] p[title="still-downloading"]')).not.toBeNull();
  });
});

describe('Queue detail drawer', () => {
  /**
   * The drawer used to be handed the row object at click time, so it froze at
   * whatever progress, rate and phase that poll had while the list underneath
   * it kept moving. It resolves the open item out of the polled store instead.
   */
  it('keeps showing the open download\'s live figures as the store polls', async () => {
    await mountQueue();

    const open = [...host.querySelectorAll('button')].find((b) =>
      b.getAttribute('aria-label')?.startsWith('Open details for still-downloading'),
    );
    expect(open, 'the row opens its drawer').toBeDefined();
    open!.click();
    flushSync();

    const drawer = () => host.querySelector('[role="dialog"]')!;
    expect(drawer().textContent).toContain('40%');

    // The same download, further along. Nothing reopens the drawer.
    queue = QUEUE.map((d) =>
      d.name === 'still-downloading' ? { ...d, progress: 0.85, down_rate: 4 * 1024 * 1024 } : d,
    );
    await downloads.refresh();
    flushSync();

    expect(drawer().textContent).toContain('85%');
    expect(drawer().textContent).toContain('4 MB/s');
  });

  it('closes itself when the open download leaves the queue', async () => {
    await mountQueue();
    const open = [...host.querySelectorAll('button')].find((b) =>
      b.getAttribute('aria-label')?.startsWith('Open details for still-downloading'),
    );
    open!.click();
    flushSync();
    expect(host.querySelector('[role="dialog"]')).not.toBeNull();

    queue = QUEUE.filter((d) => d.name !== 'still-downloading');
    await downloads.refresh();
    flushSync();

    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });
});

describe('Queue retry', () => {
  /**
   * Failed downloads used to be dead ends: the only action offered was Remove,
   * which for a Usenet release means throwing away everything it fetched.
   */
  const FAILED_USENET = download({
    name: 'failed-usenet',
    state: 'failed',
    protocol: 'usenet',
    error: 'unpacking the release failed',
  });
  const FAILED = [
    FAILED_USENET,
    download({ name: 'failed-torrent', state: 'failed', error: 'no peers' }),
  ];

  function retryButtons(): string[] {
    return [...host.querySelectorAll('li button[title="Try the failed stage again"]')].map(
      (el) => el.getAttribute('aria-label') ?? el.textContent?.trim() ?? '',
    );
  }

  it('offers retry on a failed usenet row and not on a failed torrent', async () => {
    queue = FAILED;
    await mountQueue();
    pill('All').click();
    flushSync();

    const labels = [...host.querySelectorAll('li button .sr-only')].map((el) => el.textContent?.trim());
    expect(labels).toContain('Retry failed-usenet');
    expect(labels).not.toContain('Retry failed-torrent');
    expect(retryButtons()).toHaveLength(1);
  });

  it('posts to the retry endpoint and re-polls the queue', async () => {
    queue = FAILED;
    await mountQueue();
    pill('All').click();
    flushSync();

    const retry = host.querySelector('li button[title="Try the failed stage again"]') as HTMLButtonElement;
    retry.click();
    for (let i = 0; i < 10; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
      flushSync();
    }

    const calls = (globalThis.fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls;
    const posted = calls.find(
      ([url, init]) =>
        String(url).endsWith('/retry') && (init as RequestInit | undefined)?.method === 'POST',
    );
    expect(posted, 'a POST to the retry endpoint').toBeDefined();
    expect(String(posted![0])).toContain(encodeURIComponent(FAILED_USENET.id));
  });

  it('surfaces the server refusing a retry', async () => {
    queue = FAILED;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith('/retry') && init?.method === 'POST') {
          return new Response(JSON.stringify({ error: 'only a failed download can be retried' }), {
            status: 409,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        if (url.includes('/downloads')) {
          return new Response(JSON.stringify({ downloads: queue }), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );
    await mountQueue();
    pill('All').click();
    flushSync();

    const retry = host.querySelector('li button[title="Try the failed stage again"]') as HTMLButtonElement;
    retry.click();
    for (let i = 0; i < 10; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
      flushSync();
    }

    expect(toasts.items.some((t) => t.message.includes('only a failed download can be retried'))).toBe(
      true,
    );
  });
});
