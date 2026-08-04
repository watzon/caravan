/**
 * Scan review's credential banner (PLAN phase 10 task 3).
 *
 * A scan with no usable TMDB key still runs — it walks the disk, parses every
 * name and imports what it finds — it just cannot ask TMDB which title any of
 * it is, so everything lands in this queue. Without the banner that reads as a
 * broken scanner, which is the one thing it is not.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import ScanReview from './ScanReview.svelte';
import { system } from '../state/system.svelte';
import type { SystemStatus } from '../api/types';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const STATUS = {
  version: 'test',
  mode: 'server',
  storage_root: '/data',
  schema_version: 4,
  scanning: false,
  counts: { movies: 0, series: 0, media_files: 0, unmatched: 0 },
  disk_free_bytes: 0,
  disk_total_bytes: 0,
  engine_health: 'ok',
  ffmpeg_available: true,
} as SystemStatus;

let host: HTMLElement;
let app: Record<string, unknown> | undefined;

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  system.status = null;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/import/queue')) return jsonResponse({ unmatched: [] });
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  system.status = null;
  vi.unstubAllGlobals();
});

async function settle() {
  for (let i = 0; i < 4; i += 1) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

describe('ScanReview', () => {
  it('explains an unmatched library when there is no TMDB key', async () => {
    system.status = { ...STATUS, metadata_credential: 'absent' };
    app = mount(ScanReview, { target: host });
    await settle();

    expect(host.textContent).toContain('Nothing can be matched: no TMDB API key');
    // The scan is not the thing that failed, and the copy has to say so.
    expect(host.textContent).toContain('The scan still ran');
    expect(host.querySelector('a[href="/settings/metadata"]')).not.toBeNull();
  });

  it('names a rejected key as rejected', async () => {
    system.status = { ...STATUS, metadata_credential: 'invalid' };
    app = mount(ScanReview, { target: host });
    await settle();

    expect(host.textContent).toContain('TMDB rejected your API key');
  });

  it('says nothing while the key works', async () => {
    system.status = { ...STATUS, metadata_credential: 'ok' };
    app = mount(ScanReview, { target: host });
    await settle();

    expect(host.textContent).not.toContain('Nothing can be matched');
    expect(host.textContent).toContain('Nothing to review');
  });
});
