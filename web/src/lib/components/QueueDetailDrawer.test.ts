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

function mountDrawer() {
  app = mount(QueueDetailDrawer, {
    target: host,
    props: {
      download: download(),
      onclose: vi.fn(),
      onpause: vi.fn(),
      onresume: vi.fn(),
      onremove: vi.fn(),
    },
  });
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
