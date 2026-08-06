import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import ConvertFileButton from './ConvertFileButton.svelte';
import type { MediaFile, SystemStatus, TVCompatibility } from '../api/types';
import { system } from '../state/system.svelte';

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

function file(compat: TVCompatibility): MediaFile {
  return {
    id: 42,
    path: 'library/Movies/Dune (2021)/Dune (2021).mkv',
    size: 1,
    movie_id: 7,
    quality: '2160p',
    source: 'bluray',
    codec: 'x265',
    audio: 'DTS',
    release_group: '',
    added_at: '',
    modified_at: '',
    compatibility: compat,
  };
}

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: { url: string; method: string; body: unknown }[];

function stubFetch(status = 201) {
  calls = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({
        url: String(input),
        method: init?.method ?? 'GET',
        body: typeof init?.body === 'string' ? JSON.parse(init.body) : null,
      });
      const body = status >= 400 ? { error: 'this file already has a conversion in the queue' } : { id: 1 };
      return new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
}

beforeEach(() => {
  system.status = { ...STATUS };
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  system.status = null;
  vi.unstubAllGlobals();
});

async function settle() {
  // A macrotask, not a microtask: reading the Response body is itself async.
  await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

describe('ConvertFileButton', () => {
  it('queues the file and sends the media file id', async () => {
    stubFetch();
    app = mount(ConvertFileButton, {
      target: host,
      props: { file: file({ verdict: 'incompatible', reasons: ['HEVC video'] }) },
    });
    flushSync();

    const button = host.querySelector('button') as HTMLButtonElement;
    expect(button.textContent).toContain('Convert for TV');
    // The reason travels with the button: a badge alone never explains itself.
    expect(button.title).toContain('HEVC video');

    button.click();
    await settle();

    expect(calls).toHaveLength(1);
    expect(calls[0]).toMatchObject({
      url: '/api/v1/convert',
      method: 'POST',
      body: { media_file_id: 42 },
    });
    // Once queued the affordance becomes a link to the queue, so a second
    // click cannot double-enqueue.
    expect(host.textContent).toContain('In the convert queue');
  });

  it('calls a container-only fix a conversion, because that is the user-facing action', () => {
    stubFetch();
    app = mount(ConvertFileButton, {
      target: host,
      props: { file: file({ verdict: 'needs-remux', reasons: ['MKV container'] }) },
    });
    flushSync();

    expect(host.querySelector('button')?.textContent).toContain('Convert for TV');
  });

  it('renders nothing when the profile has no complaint', () => {
    stubFetch();
    for (const verdict of ['compatible', 'unknown'] as const) {
      if (app) unmount(app);
      host.innerHTML = '';
      app = mount(ConvertFileButton, {
        target: host,
        props: { file: file({ verdict, reasons: [] }) },
      });
      flushSync();
      expect(host.querySelector('button'), verdict).toBeNull();
    }
  });

  it('renders nothing when the server has no ffmpeg', () => {
    stubFetch();
    system.status = { ...STATUS, ffmpeg_available: false };
    app = mount(ConvertFileButton, {
      target: host,
      props: { file: file({ verdict: 'incompatible', reasons: [] }) },
    });
    flushSync();

    // SPEC §8 hides the affordance rather than disabling it: a disabled button
    // with a tooltip is not hidden.
    expect(host.querySelector('button')).toBeNull();
  });

  it('treats an already-queued conflict as queued, not as an error', async () => {
    stubFetch(409);
    app = mount(ConvertFileButton, {
      target: host,
      props: { file: file({ verdict: 'incompatible', reasons: [] }) },
    });
    flushSync();

    (host.querySelector('button') as HTMLButtonElement).click();
    await settle();

    expect(host.textContent).toContain('In the convert queue');
  });
});
