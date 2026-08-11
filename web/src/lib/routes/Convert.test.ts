import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Convert from './Convert.svelte';
import type { Conversion, MediaFile, SystemStatus } from '../api/types';
import { system } from '../state/system.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const STATUS: SystemStatus = {
  version: '0.1.0',
  mode: 'server',
  storage_root: '/data',
  schema_version: 5,
  scanning: false,
  counts: { movies: 1, series: 0, media_files: 1, unmatched: 0 },
  disk_free_bytes: 1,
  disk_total_bytes: 2,
  engine_health: 'ok',
  ffmpeg_available: true,
};

const ROWS: Conversion[] = [
  {
    id: 1,
    media_file_id: 10,
    source_path: 'library/Movies/Arrival (2016)/Arrival (2016).mkv',
    output_path: 'library/Movies/Arrival (2016)/Arrival (2016).mp4',
    strategy: 'remux',
    profile_id: 'safe',
    status: 'done',
    error: '',
    created_at: '2026-08-01T00:00:00Z',
    updated_at: '2026-08-01T00:01:00Z',
  },
  {
    id: 2,
    media_file_id: 11,
    source_path: 'library/Movies/Dune (2021)/Dune (2021).mkv',
    output_path: '',
    strategy: 'transcode',
    profile_id: 'safe',
    status: 'failed',
    error: 'ffmpeg: Invalid data found when processing input',
    created_at: '2026-08-01T00:00:00Z',
    updated_at: '2026-08-01T00:02:00Z',
  },
  {
    id: 3,
    media_file_id: 12,
    source_path: 'library/Movies/Heat (1995)/Heat (1995).avi',
    output_path: '',
    strategy: '',
    profile_id: 'safe',
    status: 'queued',
    error: '',
    created_at: '2026-08-01T00:00:00Z',
    updated_at: '2026-08-01T00:00:00Z',
  },
];

const PENDING: MediaFile[] = [
  {
    id: 20,
    path: 'library/Movies/Blade Runner (1982)/Blade Runner (1982).mkv',
    size: 1_000_000,
    movie_id: 20,
    quality: '2160p',
    source: 'BluRay',
    codec: 'x265',
    audio: 'DTS',
    release_group: 'GROUP',
    added_at: '2026-08-01T00:00:00Z',
    modified_at: '2026-08-01T00:00:00Z',
    compatibility: {
      verdict: 'incompatible',
      reasons: ['HEVC video', 'DTS audio'],
    },
  },
  {
    id: 21,
    path: 'library/Movies/Alien (1979)/Alien (1979).mkv',
    size: 2_000_000,
    movie_id: 21,
    quality: '1080p',
    source: 'BluRay',
    codec: 'x264',
    audio: 'AAC',
    release_group: 'GROUP',
    added_at: '2026-08-01T00:00:00Z',
    modified_at: '2026-08-01T00:00:00Z',
    compatibility: {
      verdict: 'needs-remux',
      reasons: ['MKV container'],
    },
  },
];

let host: HTMLElement;
let app: Record<string, unknown>;
let posts: { url: string; body: unknown }[];
let rows: Conversion[];
let pending: MediaFile[];
let queueLoads: number;
let conversionReleases: Array<() => void>;

interface StubOptions {
  conversionStatuses?: Record<number, number>;
  holdConversions?: boolean;
}

function stub(ffmpeg: boolean, options: StubOptions = {}) {
  posts = [];
  queueLoads = 0;
  conversionReleases = [];
  system.status = { ...STATUS, ffmpeg_available: ffmpeg };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if ((init?.method ?? 'GET') === 'POST') {
        const body = init?.body ? JSON.parse(String(init.body)) : null;
        posts.push({ url, body });
        if (url.endsWith('/convert')) {
          const mediaFileID = Number(body.media_file_id);
          if (options.holdConversions) {
            await new Promise<void>((resolve) => conversionReleases.push(resolve));
          }
          const status = options.conversionStatuses?.[mediaFileID] ?? 200;
          const file = pending.find((candidate) => candidate.id === mediaFileID)!;
          if (status >= 400) {
            if (status === 409) {
              pending = pending.filter((candidate) => candidate.id !== mediaFileID);
            }
            return jsonResponse(
              {
                error: status === 409
                  ? 'this file already has a conversion in the queue'
                  : 'conversion failed',
              },
              status,
            );
          }
          pending = pending.filter((candidate) => candidate.id !== mediaFileID);
          const queued: Conversion = {
            id: 4,
            media_file_id: file.id,
            source_path: file.path,
            output_path: '',
            strategy: '',
            profile_id: 'safe',
            status: 'queued',
            error: '',
            created_at: '2026-08-01T00:03:00Z',
            updated_at: '2026-08-01T00:03:00Z',
          };
          rows = [queued, ...rows];
          return jsonResponse(queued);
        }
        return jsonResponse({ ...rows[2], status: 'cancelled' });
      }
      if (url.includes('/convert')) {
        queueLoads++;
        return jsonResponse({ pending, conversions: rows });
      }
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
}

beforeEach(() => {
  rows = ROWS.map((r) => ({ ...r }));
  pending = PENDING.map((file) => ({
    ...file,
    compatibility: { ...file.compatibility, reasons: [...file.compatibility.reasons] },
  }));
  clearToasts();
  vi.useFakeTimers();
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  unmount(app);
  host.remove();
  system.status = null;
  vi.unstubAllGlobals();
  clearToasts();
  vi.useRealTimers();
});

async function settle() {
  await vi.advanceTimersByTimeAsync(0);
  await Promise.resolve();
  flushSync();
}

function buttonWith(text: string): HTMLButtonElement | undefined {
  return [...host.querySelectorAll('button')].find((b) => b.textContent?.includes(text)) as
    | HTMLButtonElement
    | undefined;
}

function buttonLabeled(text: string): HTMLButtonElement | undefined {
  return [...host.querySelectorAll<HTMLButtonElement>('button[aria-label]')].find((button) =>
    button.getAttribute('aria-label')?.includes(text),
  );
}

function selectionBar(): HTMLElement | null {
  return host.querySelector('[role="group"][aria-label="Selection actions"]');
}

function tabWith(text: string): HTMLButtonElement | undefined {
  return [...host.querySelectorAll<HTMLButtonElement>('[role="group"][aria-label="Conversion work"] button')].find(
    (button) => button.textContent?.includes(text),
  );
}

describe('Convert route', () => {
  it('shows pending files first and queues them from the page', async () => {
    stub(true);
    app = mount(Convert, { target: host });
    await settle();

    expect(host.textContent).toContain('Blade Runner (1982).mkv');
    expect(host.textContent).toContain('HEVC video');
    expect(host.textContent).not.toContain('Arrival (2016).mkv');
    expect(host.textContent).not.toContain('REMUX');
    expect(host.querySelector(`[title="${PENDING[0]?.path}"]`)).not.toBeNull();
    expect(
      [...host.querySelectorAll<HTMLElement>('[title]')].some((element) =>
        element.title.toLowerCase().includes('remux'),
      ),
    ).toBe(false);
    expect(tabWith('Pending')?.textContent).toContain('2');
    expect(tabWith('Active')?.textContent).toContain('1');
    expect(tabWith('Finished')?.textContent).toContain('2');
    expect(tabWith('Pending')?.getAttribute('aria-pressed')).toBe('true');
    expect(tabWith('Active')?.getAttribute('aria-pressed')).toBe('false');

    buttonWith('Convert for TV')!.click();
    await settle();
    await settle();

    expect(posts).toContainEqual({
      url: '/api/v1/convert',
      body: { media_file_id: 20 },
    });
    expect(host.textContent).not.toContain('Blade Runner (1982).mkv');
    expect(tabWith('Pending')?.textContent).toContain('1');

    tabWith('Active')!.click();
    flushSync();
    expect(tabWith('Active')?.getAttribute('aria-pressed')).toBe('true');
    expect(host.textContent).toContain('Blade Runner (1982).mkv');
    expect(host.textContent).toContain('Deciding...');
  });

  it('queues two selected files sequentially, then reloads and toasts once', async () => {
    stub(true, { holdConversions: true });
    app = mount(Convert, { target: host });
    await settle();

    buttonLabeled('Select Blade Runner')!.click();
    flushSync();
    buttonLabeled('Select Alien')!.click();
    flushSync();
    expect(selectionBar()?.textContent).toContain('2 selected');

    buttonWith('Convert selected')!.click();
    await Promise.resolve();
    expect(posts.filter((post) => post.url === '/api/v1/convert')).toEqual([
      { url: '/api/v1/convert', body: { media_file_id: 20 } },
    ]);

    conversionReleases.shift()!();
    await settle();
    expect(posts.filter((post) => post.url === '/api/v1/convert')).toEqual([
      { url: '/api/v1/convert', body: { media_file_id: 20 } },
      { url: '/api/v1/convert', body: { media_file_id: 21 } },
    ]);

    conversionReleases.shift()!();
    await settle();
    await settle();

    expect(queueLoads).toBe(2);
    expect(toasts.items).toEqual([
      expect.objectContaining({ message: 'Queued 2', tone: 'neutral' }),
    ]);
    expect(selectionBar()).toBeNull();
  });

  it('treats an already-queued response as a handled selection', async () => {
    stub(true, { conversionStatuses: { 20: 409 } });
    app = mount(Convert, { target: host });
    await settle();

    buttonLabeled('Select Blade Runner')!.click();
    flushSync();
    buttonWith('Convert selected')!.click();
    await settle();
    await settle();

    expect(queueLoads).toBe(2);
    expect(toasts.items).toEqual([
      expect.objectContaining({ message: 'Queued 1', tone: 'neutral' }),
    ]);
    expect(selectionBar()).toBeNull();
    expect(host.textContent).not.toContain('Blade Runner (1982).mkv');
  });

  it('retains only failed selections after a partial batch', async () => {
    stub(true, { conversionStatuses: { 21: 500 } });
    app = mount(Convert, { target: host });
    await settle();

    buttonLabeled('Select Blade Runner')!.click();
    flushSync();
    buttonLabeled('Select Alien')!.click();
    flushSync();
    buttonWith('Convert selected')!.click();
    await settle();
    await settle();

    expect(
      posts.filter((post) => post.url === '/api/v1/convert').map((post) => post.body),
    ).toEqual([
      { media_file_id: 20 },
      { media_file_id: 21 },
    ]);
    expect(queueLoads).toBe(2);
    expect(toasts.items).toEqual([
      expect.objectContaining({ message: 'Queued 1 of 2', tone: 'danger' }),
    ]);
    expect(selectionBar()?.textContent).toContain('1 selected');
    expect(buttonLabeled('Deselect Alien')?.getAttribute('aria-pressed')).toBe('true');
    expect(host.textContent).not.toContain('Blade Runner (1982).mkv');
  });

  it('clears pending selections from Escape and the bar control', async () => {
    stub(true);
    app = mount(Convert, { target: host });
    await settle();

    buttonLabeled('Select Blade Runner')!.click();
    flushSync();
    expect(selectionBar()).not.toBeNull();

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    flushSync();
    expect(selectionBar()).toBeNull();

    buttonLabeled('Select Alien')!.click();
    flushSync();
    host.querySelector<HTMLButtonElement>('button[title="Clear selection"]')!.click();
    flushSync();
    expect(selectionBar()).toBeNull();
  });

  it('separates active work from finished history and keeps their actions', async () => {
    stub(true);
    app = mount(Convert, { target: host });
    await settle();

    tabWith('Active')!.click();
    flushSync();
    expect(host.textContent).toContain('Heat (1995).avi');
    expect(host.textContent).not.toContain('Arrival (2016).mkv');
    buttonWith('Cancel')!.click();
    await settle();
    expect(posts.map((post) => post.url)).toContain('/api/v1/convert/3/cancel');

    tabWith('Finished')!.click();
    flushSync();
    expect(host.textContent).toContain('Arrival (2016).mkv');
    expect(host.textContent).toContain('Convert (stream copy)');
    expect(host.textContent).toContain('Transcode (re-encode)');
    expect(host.textContent).not.toContain('Remux');
    expect(host.textContent).toContain('ffmpeg: Invalid data found when processing input');
    expect(host.textContent).not.toContain('Heat (1995).avi');
    const finishedOpen = host.querySelector<HTMLButtonElement>(
      'button[aria-label^="Open conversion details for Arrival"]',
    );
    expect(finishedOpen, 'the finished row opens its detail drawer').not.toBeNull();
    finishedOpen!.click();
    flushSync();
    expect(host.querySelector('[role="dialog"]')?.textContent).toContain('Done');
    expect(host.querySelector('[role="dialog"]')?.textContent).toContain(
      'Arrival (2016).mp4',
    );
    buttonWith('Close')!.click();
    flushSync();
    expect(host.querySelector('[role="dialog"]')).toBeNull();

    buttonWith('Retry')!.click();
    await settle();
    expect(posts.map((post) => post.url)).toContain('/api/v1/convert/2/retry');
  });

  it('opens live job details and keeps them current across polls', async () => {
    rows[2] = {
      ...rows[2]!,
      status: 'running',
      strategy: 'transcode',
      stage: 'converting',
      started_at: '2026-08-05T12:00:00Z',
      progress: 0.5,
      processed_seconds: 60,
      duration_seconds: 120,
      speed: 1.5,
      eta_seconds: 40,
    };
    stub(true);
    app = mount(Convert, { target: host });
    await settle();

    tabWith('Active')!.click();
    flushSync();
    const open = host.querySelector<HTMLButtonElement>(
      'button[aria-label^="Open conversion details for Heat"]',
    );
    expect(open, 'the active row opens its detail drawer').not.toBeNull();
    open!.click();
    flushSync();

    const drawer = () => host.querySelector('[role="dialog"]')!;
    expect(drawer().textContent).toContain('Encoding media');
    expect(drawer().textContent).toContain('50%');
    expect(drawer().textContent).toContain('40s');

    rows = rows.map((row) =>
      row.id === 3
        ? { ...row, progress: 0.75, processed_seconds: 90, eta_seconds: 20 }
        : row,
    );
    await vi.advanceTimersByTimeAsync(5000);
    flushSync();

    expect(drawer().textContent).toContain('75%');
    expect(drawer().textContent).toContain('20s');

    rows = rows.filter((row) => row.id !== 3);
    await vi.advanceTimersByTimeAsync(5000);
    flushSync();
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });

  it('keeps the newest conversion response when requests finish out of order', async () => {
    const active: Conversion = {
      ...ROWS[2]!,
      status: 'running',
      strategy: 'transcode',
      stage: 'converting',
      started_at: '2026-08-05T12:00:00Z',
      progress: 0.25,
      processed_seconds: 30,
      duration_seconds: 120,
      speed: 1,
      eta_seconds: 90,
    };
    const deferred: Array<(body: {
      pending: MediaFile[];
      conversions: Conversion[];
    }) => void> = [];
    let requestCount = 0;
    system.status = { ...STATUS };
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL) => {
        if (!String(input).includes('/convert')) {
          return Promise.reject(new Error(`unexpected fetch: ${String(input)}`));
        }
        requestCount += 1;
        if (requestCount === 1) {
          return Promise.resolve(jsonResponse({ pending: [], conversions: [active] }));
        }
        return new Promise<Response>((resolve) => {
          deferred.push((body) => resolve(jsonResponse(body)));
        });
      }),
    );

    app = mount(Convert, { target: host });
    await settle();
    tabWith('Active')!.click();
    flushSync();
    host.querySelector<HTMLButtonElement>(
      'button[aria-label^="Open conversion details for Heat"]',
    )!.click();
    flushSync();

    buttonWith('Refresh')!.click();
    buttonWith('Refresh')!.click();
    expect(deferred).toHaveLength(2);

    deferred[1]!({
      pending: [],
      conversions: [{
        ...active,
        progress: 0.75,
        processed_seconds: 90,
        eta_seconds: 30,
      }],
    });
    await settle();
    expect(host.querySelector('[role="dialog"]')?.textContent).toContain('75%');

    deferred[0]!({
      pending: [],
      conversions: [{
        ...active,
        progress: 0.5,
        processed_seconds: 60,
        eta_seconds: 60,
      }],
    });
    await settle();
    expect(host.querySelector('[role="dialog"]')?.textContent).toContain('75%');
    expect(host.querySelector('[role="dialog"]')?.textContent).not.toContain('50%');
  });

  it('degrades to an informational banner when ffmpeg is missing', async () => {
    stub(false);
    app = mount(Convert, { target: host });
    await settle();

    expect(host.textContent).toContain('ffmpeg is not installed');
    expect(host.textContent).toContain('Blade Runner (1982).mkv');
    expect(buttonWith('Convert for TV')).toBeUndefined();
    expect(buttonLabeled('Select Blade Runner')).toBeUndefined();
    expect(selectionBar()).toBeNull();

    tabWith('Finished')!.click();
    flushSync();
    // History stays readable - uninstalling ffmpeg must not erase it.
    expect(host.textContent).toContain('Arrival (2016).mkv');
    expect(buttonWith('Retry')).toBeUndefined();
    host.querySelector<HTMLButtonElement>(
      'button[aria-label^="Open conversion details for Dune"]',
    )!.click();
    flushSync();
    const drawerRetry = [...host.querySelectorAll<HTMLButtonElement>(
      '[role="dialog"] button',
    )].find((button) => button.textContent?.includes('Retry'));
    expect(drawerRetry).toBeUndefined();
  });

  it('shows an empty state rather than a blank screen', async () => {
    rows = [];
    pending = [];
    stub(true);
    app = mount(Convert, { target: host });
    await settle();

    expect(host.textContent).toContain('No files need conversion');
  });
});
