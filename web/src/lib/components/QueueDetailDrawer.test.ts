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

function mountDrawer(overrides: Partial<DownloadStatus> = {}, props: Record<string, unknown> = {}) {
  app = mount(QueueDetailDrawer, {
    target: host,
    props: {
      download: download(overrides),
      onclose: vi.fn(),
      onpause: vi.fn(),
      onresume: vi.fn(),
      onremove: vi.fn(),
      ...props,
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
    const [down, up] = [...host.querySelectorAll<HTMLInputElement>('input[type="number"]')];
    expect(down).toBeDefined();
    expect(up).toBeDefined();
    if (!down || !up) throw new Error('Expected download and upload rate inputs');
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

  it('labels rate inputs with their units and shared help', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      jsonResponse({ insight: { availability: 1, peers: [], trackers: [] } }),
    ));
    mountDrawer();
    await settle();

    button('Limits').click();
    flushSync();
    const inputs = [...host.querySelectorAll<HTMLInputElement>('input[type="number"]')];
    expect(inputs).toHaveLength(2);
    for (const input of inputs) {
      expect(input.labels?.[0]?.textContent).toContain('KB/s');
      const descriptionID = input.getAttribute('aria-describedby');
      expect(descriptionID).not.toBeNull();
      expect(host.querySelector(`#${descriptionID}`)?.textContent).toContain('0 is unlimited');
    }
  });

  it('names the dialog from the full title, exposes progress text, and traps focus', async () => {
    const name = 'An intentionally long release name that truncates in the drawer header';
    const onclose = vi.fn();
    vi.stubGlobal('fetch', vi.fn(async () =>
      jsonResponse({ insight: { availability: 1, peers: [], trackers: [] } }),
    ));
    mountDrawer({ name }, { onclose });
    await settle();

    const dialog = host.querySelector<HTMLElement>('[role="dialog"]')!;
    const headingID = dialog.getAttribute('aria-labelledby');
    const heading = headingID ? host.querySelector<HTMLElement>(`#${headingID}`) : null;
    const backdrop = host.querySelector<HTMLButtonElement>('button[aria-hidden="true"]')!;
    const progress = host.querySelector<HTMLElement>('[role="progressbar"]')!;

    expect(heading?.textContent?.trim()).toBe(name);
    expect(heading?.title).toBe(name);
    expect(backdrop.tabIndex).toBe(-1);
    expect(progress.getAttribute('aria-label')).toBe(`${name} progress`);
    expect(progress.getAttribute('aria-valuetext')).toBe('50%');
    expect(document.activeElement).toBe(dialog);

    const focusable = [...dialog.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])',
    )].filter((element) => element.tabIndex >= 0);
    dialog.dispatchEvent(new KeyboardEvent('keydown', {
      key: 'Tab',
      shiftKey: true,
      bubbles: true,
      cancelable: true,
    }));
    expect(document.activeElement).toBe(focusable.at(-1));

    focusable.at(-1)?.dispatchEvent(new KeyboardEvent('keydown', {
      key: 'Tab',
      bubbles: true,
      cancelable: true,
    }));
    expect(document.activeElement).toBe(focusable[0]);

    dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(onclose).toHaveBeenCalledOnce();
  });

  it('links tabs to their panels and moves selection with arrow keys', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      jsonResponse({ insight: { availability: 1, peers: [], trackers: [] } }),
    ));
    mountDrawer();
    await settle();

    const tabs = [...host.querySelectorAll<HTMLButtonElement>('[role="tab"]')];
    const peers = tabs.find((candidate) => candidate.textContent?.includes('Peers'))!;
    const trackers = tabs.find((candidate) => candidate.textContent?.includes('Trackers'))!;
    expect(peers.tabIndex).toBe(0);
    expect(trackers.tabIndex).toBe(-1);
    expect(host.querySelector(`#${peers.getAttribute('aria-controls')}`)?.getAttribute('aria-labelledby'))
      .toBe(peers.id);

    peers.focus();
    peers.dispatchEvent(new KeyboardEvent('keydown', {
      key: 'ArrowRight',
      bubbles: true,
      cancelable: true,
    }));
    flushSync();
    expect(document.activeElement).toBe(trackers);
    expect(trackers.getAttribute('aria-selected')).toBe('true');
    expect(host.querySelector(`#${trackers.getAttribute('aria-controls')}`)?.getAttribute('aria-labelledby'))
      .toBe(trackers.id);
  });

  it('links a queued download to concurrency settings', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({ insight: { availability: 1, peers: [], trackers: [] } })));
    mountDrawer({ state: 'queued' });
    await settle();

    const link = host.querySelector<HTMLAnchorElement>('a[href="/settings/downloads#download-concurrency"]');
    expect(link?.textContent?.trim()).toBe('Settings → Downloads → Concurrency');
  });

  it('links rate-limit help to concurrency settings', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({ insight: { availability: 1, peers: [], trackers: [] } })));
    mountDrawer();
    await settle();

    button('Limits').click();
    flushSync();

    const link = host.querySelector<HTMLAnchorElement>('a[href="/settings/downloads#download-concurrency"]');
    expect(link?.textContent?.trim()).toBe('Settings → Downloads → Concurrency');
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
    expect(host.querySelector('input[type="number"]')).toBeNull();
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

describe('QueueDetailDrawer retry', () => {
  // A failed Usenet download has nothing to pause — it has a stage that went
  // wrong and gigabytes already on disk, so Retry takes Pause's place rather
  // than sitting beside a button that would do nothing.
  it('offers retry instead of pause for a failed usenet download', async () => {
    const onretry = vi.fn();
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(usenetInsight())));
    mountDrawer({ protocol: 'usenet', state: 'failed', error: 'unpacking the release failed' }, { onretry });
    await settle();

    expect(host.textContent).toContain('Retry');
    expect(host.textContent).not.toContain('Pause');

    button('Retry').click();
    flushSync();
    expect(onretry).toHaveBeenCalledTimes(1);
  });

  // A torrent engine's failures are about the swarm and it implements no retry
  // capability, so the drawer must not offer one.
  it('does not offer retry for a failed torrent', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      jsonResponse({ insight: { peers: [], trackers: [], availability: 0 } }),
    ));
    mountDrawer({ state: 'failed', error: 'no peers' }, { onretry: vi.fn() });
    await settle();

    expect(host.textContent).not.toContain('Retry');
    expect(host.textContent).toContain('Pause');
  });

  // And a usenet download that is merely downloading has nothing to retry.
  it('does not offer retry for a healthy usenet download', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(usenetInsight())));
    mountDrawer({ protocol: 'usenet', state: 'downloading' }, { onretry: vi.fn() });
    await settle();

    expect(host.textContent).not.toContain('Retry');
    expect(host.textContent).toContain('Pause');
  });
});

describe('QueueDetailDrawer queued-by-cap', () => {
  // A queued download whose size is already known is waiting on the
  // concurrency cap, not on a magnet's metadata. Saying so is the difference
  // between "my queue works" and "my queue is stuck".
  it('says a queued download is waiting for a slot', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(usenetInsight())));
    mountDrawer({ protocol: 'usenet', state: 'queued', down_rate: 0 });
    await settle();

    expect(host.textContent).toContain('Waiting for a free download slot');
    expect(host.textContent).toContain('Concurrency');
  });

  // A torrent with no metadata yet is queued for a different reason, and
  // blaming the cap there would send the user to the wrong screen.
  it('stays quiet for a torrent that has no metadata yet', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      jsonResponse({ insight: { peers: [], trackers: [], availability: 0 } }),
    ));
    mountDrawer({ state: 'queued', size: 0, bytes_done: 0 });
    await settle();

    expect(host.textContent).not.toContain('Waiting for a free download slot');
  });

  it('stays quiet while a download is actually running', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(usenetInsight())));
    mountDrawer({ protocol: 'usenet', state: 'downloading' });
    await settle();

    expect(host.textContent).not.toContain('Waiting for a free download slot');
  });
});
