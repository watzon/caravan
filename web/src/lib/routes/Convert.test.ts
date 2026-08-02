import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Convert from './Convert.svelte';
import type { Conversion, SystemStatus } from '../api/types';
import { system } from '../state/system.svelte';

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
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

let host: HTMLElement;
let app: Record<string, unknown>;
let posts: string[];
let rows: Conversion[];

function stub(ffmpeg: boolean) {
  posts = [];
  system.status = { ...STATUS, ffmpeg_available: ffmpeg };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if ((init?.method ?? 'GET') === 'POST') {
        posts.push(url);
        return jsonResponse({ ...rows[2], status: 'cancelled' });
      }
      if (url.includes('/convert')) return jsonResponse({ conversions: rows });
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
}

beforeEach(() => {
  rows = ROWS.map((r) => ({ ...r }));
  vi.useFakeTimers();
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  unmount(app);
  host.remove();
  system.status = null;
  vi.unstubAllGlobals();
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

describe('Convert route', () => {
  it('renders the queue with the strategy and failure spelled out', async () => {
    stub(true);
    app = mount(Convert, { target: host });
    await settle();

    expect(host.textContent).toContain('Arrival (2016).mkv');
    // The strategy label has to say what it costs, not just its name.
    expect(host.textContent).toContain('Remux (stream copy)');
    expect(host.textContent).toContain('Transcode (re-encode)');
    // A queued row has not been probed yet, so no strategy is claimed.
    expect(host.textContent).toContain('Deciding…');
    // SPEC §13: failures are visible, with the reason attached.
    expect(host.textContent).toContain('ffmpeg: Invalid data found when processing input');
  });

  it('offers cancel on a queued row and retry on a failed one', async () => {
    stub(true);
    app = mount(Convert, { target: host });
    await settle();

    const cancel = buttonWith('Cancel');
    expect(cancel).toBeDefined();
    cancel!.click();
    await settle();
    expect(posts).toContain('/api/v1/convert/3/cancel');

    const retry = buttonWith('Retry');
    expect(retry).toBeDefined();
    retry!.click();
    await settle();
    expect(posts).toContain('/api/v1/convert/2/retry');
  });

  it('degrades to an informational banner when ffmpeg is missing', async () => {
    stub(false);
    app = mount(Convert, { target: host });
    await settle();

    expect(host.textContent).toContain('ffmpeg is not installed');
    // History stays readable — uninstalling ffmpeg must not erase it.
    expect(host.textContent).toContain('Arrival (2016).mkv');
    // But nothing can be re-queued.
    expect(buttonWith('Retry')?.disabled).toBe(true);
  });

  it('shows an empty state rather than a blank screen', async () => {
    rows = [];
    stub(true);
    app = mount(Convert, { target: host });
    await settle();

    expect(host.textContent).toContain('Nothing to convert');
  });
});
