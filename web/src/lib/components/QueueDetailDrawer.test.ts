import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import type { DownloadStatus } from '../api/types';
import QueueDetailDrawer from './QueueDetailDrawer.svelte';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

function download(overrides: Partial<DownloadStatus> = {}): DownloadStatus {
  return {
    id: 'hash-a',
    state: 'downloading',
    name: 'Example.Release.1080p-GROUP',
    progress: 0.5,
    bytes_done: 5 * 1024 ** 3,
    size: 10 * 1024 ** 3,
    down_rate: 128 * 1024,
    up_rate: 32 * 1024,
    eta_seconds: 120,
    ratio: 1.25,
    save_path: 'incomplete/example',
    error: '',
    max_down_rate: 2 * 1024,
    max_up_rate: 4 * 1024,
    ...overrides,
  };
}

let host: HTMLElement;
let app: Record<string, unknown>;

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  vi.useFakeTimers();
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

function button(label: string) {
  const found = [...host.querySelectorAll('button')].find((candidate) => candidate.textContent?.includes(label));
  expect(found, `button labelled ${label}`).toBeDefined();
  return found!;
}

function mountDrawer(overrides: Partial<DownloadStatus> = {}) {
  app = mount(QueueDetailDrawer, {
    target: host,
    props: {
      download: download(overrides),
      onclose: vi.fn(),
      onpause: vi.fn(),
      onresume: vi.fn(),
      onremove: vi.fn(),
    },
  });
}

/** A Usenet insight body: the file half, and no peers or trackers. */
function usenetInsight(overrides: Record<string, unknown> = {}) {
  return {
    insight: {
      peers: [],
      trackers: [],
      availability: 0,
      files: [
        { name: 'movie.mkv', segments: 40, segments_done: 18, segments_failed: 0, complete: false, par2: false },
        { name: 'movie.nfo', segments: 1, segments_done: 1, segments_failed: 0, complete: true, par2: false },
      ],
      files_complete: 1,
      segments: 41,
      segments_done: 19,
      ...overrides,
    },
  };
}

describe('QueueDetailDrawer', () => {
  it('opens with mapped peer, tracker and availability insight', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      jsonResponse({
        insight: {
          availability: 3.18,
          peers: [
            {
              addr: '192.0.2.44:51413',
              client: 'Caravan Test Peer',
              progress: 0.75,
              down_rate: 64 * 1024,
              up_rate: 8 * 1024,
            },
          ],
          trackers: [
            {
              url: 'https://tracker.example/announce',
              status: 'working',
              seeders: 11,
              leechers: 7,
            },
          ],
        },
      }),
    ));
    mountDrawer();
    await settle();

    expect(host.textContent).toContain('3.18');
    expect(host.textContent).toContain('192.0.2.44:51413');
    expect(host.textContent).toContain('Caravan Test Peer');
    expect(host.textContent).toContain('75%');

    button('Trackers').click();
    flushSync();
    expect(host.textContent).toContain('https://tracker.example/announce');
    expect(host.textContent).toContain('11 S / 7 L');
  });

  it('converts byte limits to KB/s and writes KB/s values', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (!init?.method || init.method === 'GET') {
        return jsonResponse({ insight: { availability: 1, peers: [], trackers: [] } });
      }
      return new Response(null, { status: 204 });
    });
    vi.stubGlobal('fetch', fetchMock);
    mountDrawer();
    await settle();

    button('Limits').click();
    flushSync();
    const down = host.querySelector('input[aria-label="Download limit"]') as HTMLInputElement;
    const up = host.querySelector('input[aria-label="Upload limit"]') as HTMLInputElement;
    expect(down.value).toBe('2');
    expect(up.value).toBe('4');

    down.value = '1536';
    down.dispatchEvent(new Event('input', { bubbles: true }));
    up.value = '256';
    up.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    button('Apply limits').click();
    await settle();

    expect(fetchMock).toHaveBeenLastCalledWith(
      '/api/v1/downloads/hash-a/limits',
      expect.objectContaining({ method: 'PUT', body: JSON.stringify({ max_down_kbps: 1536, max_up_kbps: 256 }) }),
    );
  });

  it('degrades to limits when insight is unsupported', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({ error: 'unsupported' }, 400)));
    mountDrawer();
    await settle();

    expect(host.textContent).toContain('Limits');
    expect(host.textContent).not.toContain('Peers');
    expect(host.textContent).not.toContain('Trackers');
  });
});

describe('QueueDetailDrawer (usenet)', () => {
  // The complaint this split fixes: a Usenet download used to show a torrent's
  // upload rate, share ratio and piece availability — all structurally zero —
  // plus a Limits tab whose Apply button the embedded engine answers 400 for.
  it('drops every torrent-only figure, tab and control', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(usenetInsight())));
    mountDrawer({ protocol: 'usenet', engine: 'embedded-usenet' });
    await settle();

    // The stats row carries only the two figures a Usenet download has.
    const stats = [...host.querySelectorAll('dt.micro-label')].map((el) => el.textContent?.trim());
    expect(stats).toEqual(['Down', 'ETA', 'Client', 'Location']);

    const tabs = [...host.querySelectorAll('[role="tab"]')].map((el) => el.textContent?.trim());
    expect(tabs).toEqual(['Files (2)']);
    expect(host.querySelector('input[aria-label="Upload limit"]')).toBeNull();
    expect(host.querySelector('input[aria-label="Download limit"]')).toBeNull();
    expect(host.textContent).not.toContain('Seeding targets');
  });

  it('lists each file in the NZB with its own segment progress', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(usenetInsight())));
    mountDrawer({ protocol: 'usenet' });
    await settle();

    expect(host.textContent).toContain('Files (2)');
    expect(host.textContent).toContain('movie.mkv');
    expect(host.textContent).toContain('18 / 40 segments');
    expect(host.textContent).toContain('movie.nfo');
    expect(host.textContent).toContain('1 / 1 segments');
    expect(host.textContent).toContain('Complete');
  });

  it('names the damaged files a repair is working through', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      jsonResponse(usenetInsight({ damaged_segments: 3, damaged_files: ['movie.mkv'] })),
    ));
    mountDrawer({ protocol: 'usenet', phase: 'repairing' });
    await settle();

    expect(host.textContent).toContain('Repairing');
    expect(host.textContent).toContain('3 segments to reconstruct');
  });

  it('polls insight while the drawer is open, whatever tab is showing', async () => {
    const fetchMock = vi.fn(async () => jsonResponse(usenetInsight()));
    vi.stubGlobal('fetch', fetchMock);
    mountDrawer({ protocol: 'usenet' });
    await settle();
    const initial = fetchMock.mock.calls.length;

    // A Usenet download's files and phase change under the drawer with no tab
    // interaction at all, so the poll cannot be gated on one.
    await vi.advanceTimersByTimeAsync(3000);
    expect(fetchMock.mock.calls.length).toBeGreaterThan(initial);
  });
});
