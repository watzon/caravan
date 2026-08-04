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
    ...overrides,
  };
}

const QUEUE: DownloadStatus[] = [
  download({ name: 'still-downloading', state: 'downloading', progress: 0.4 }),
  download({ name: 'paused-mid-download', state: 'paused', progress: 0.6 }),
  download({ name: 'seeding-away', state: 'seeding', progress: 1 }),
  download({ name: 'imported-and-done', state: 'completed', progress: 1 }),
  download({ name: 'finished-parked-torrent', state: 'paused', progress: 1 }),
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
    expect(rowNames()).toEqual(['still-downloading', 'seeding-away', 'paused-mid-download']);
  });

  it('shows the finished bucket under Done', async () => {
    await mountQueue();
    pill('Done').click();
    flushSync();
    expect(rowNames()).toEqual(['finished-parked-torrent', 'imported-and-done']);
  });

  it('shows everything under All', async () => {
    await mountQueue();
    pill('All').click();
    flushSync();
    expect(rowNames()).toHaveLength(QUEUE.length);
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
