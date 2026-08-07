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
import { libraries } from '../state/libraries.svelte';
import type { Library, SystemStatus, UnmatchedFile } from '../api/types';

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
let unmatched: UnmatchedFile[];

beforeEach(() => {
  unmatched = [];
  host = document.createElement('div');
  document.body.appendChild(host);
  system.status = null;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/import/queue')) return jsonResponse({ items: unmatched });
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  system.status = null;
  libraries.all = [];
  libraries.loaded = false;
  vi.unstubAllGlobals();
});

function library(overrides: Partial<Library> = {}): Library {
  return {
    id: 4,
    kind: 'movie',
    name: 'Kids Movies',
    root_path: 'library/Kids',
    provider: 'tmdb',
    providers: ['tmdb'],
    is_default: false,
    item_count: 0,
    dlna_visible: true,
    route_torrent: '',
    route_usenet: '',
    quality_profile_id: 0,
    indexers: [],
    ...overrides,
  };
}

function parkedFile(overrides: Partial<UnmatchedFile> = {}): UnmatchedFile {
  return {
    id: 1,
    path: 'downloads/Some.Release.mkv',
    size: 1_000_000,
    parsed: {
      title: 'Some Release',
      year: 2026,
      season: 0,
      episodes: [],
      quality: '1080p',
      source: 'WEB-DL',
      codec: 'x264',
      audio: 'AC3',
      bit_depth: 8,
      group: 'GROUP',
      proper: false,
      repack: false,
      edition: '',
      confidence: 0.4,
    },
    reason: 'No confident match',
    library_id: 0,
    seen_at: '2026-08-01T00:00:00Z',
    ...overrides,
  };
}

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

  it('exposes a full unmatched path when the table shortens it', async () => {
    const path = 'library/Movies/A Very Long Movie Name (2026)/A.Very.Long.Movie.Name.2026.2160p.BluRay.x265-GROUP.mkv';
    unmatched = [{
      id: 1,
      path,
      size: 1_000_000,
      parsed: {
        title: 'A Very Long Movie Name',
        year: 2026,
        season: 0,
        episodes: [],
        quality: '2160p',
        source: 'BluRay',
        codec: 'x265',
        audio: 'DTS',
        bit_depth: 10,
        group: 'GROUP',
        proper: false,
        repack: false,
        edition: '',
        confidence: 0.4,
      },
      reason: 'No confident match',
      library_id: 0,
      seen_at: '2026-08-01T00:00:00Z',
    }];
    system.status = { ...STATUS, metadata_credential: 'ok' };
    app = mount(ScanReview, { target: host });
    await settle();

    const pathCell = host.querySelector<HTMLElement>('td.font-mono[title]');
    expect(pathCell).not.toBeNull();
    expect(pathCell?.textContent?.trim()).not.toBe(path);
    expect(pathCell?.getAttribute('title')).toBe(path);
  });

  /**
   * The universal search's untied grab parks here on purpose (plan part B8),
   * and it arrives knowing two things a scan-parked file does not: which
   * library the user chose, and that nothing actually failed.
   */
  it('names the library an untied grab was scoped to', async () => {
    libraries.all = [library()];
    libraries.loaded = true;
    unmatched = [parkedFile({ library_id: 4, reason: 'manual-grab' })];
    system.status = { ...STATUS, metadata_credential: 'ok' };
    app = mount(ScanReview, { target: host });
    await settle();

    expect(host.textContent).toContain('Kids Movies');
  });

  it('reads the manual-grab reason as what it is, not as a scanner complaint', async () => {
    unmatched = [parkedFile({ library_id: 4, reason: 'manual-grab' })];
    system.status = { ...STATUS, metadata_credential: 'ok' };
    app = mount(ScanReview, { target: host });
    await settle();

    expect(host.textContent).toContain('Grabbed manually');
    expect(host.textContent).not.toContain('manual-grab');
  });

  it('leaves a scan-parked file unscoped rather than inventing a library', async () => {
    libraries.all = [library()];
    libraries.loaded = true;
    unmatched = [parkedFile()];
    system.status = { ...STATUS, metadata_credential: 'ok' };
    app = mount(ScanReview, { target: host });
    await settle();

    expect(host.textContent).not.toContain('Kids Movies');
    expect(host.textContent).toContain('No confident match');
  });

  /**
   * A library has exactly one kind, and the user already chose it. That beats
   * the parser's guess, which here says "movie" because the name has no SxxEyy.
   */
  it('pre-selects the match scope from the library an untied grab chose', async () => {
    libraries.all = [library({ id: 5, kind: 'tv', name: 'TV' })];
    libraries.loaded = true;
    unmatched = [parkedFile({ library_id: 5, reason: 'manual-grab' })];
    system.status = { ...STATUS, metadata_credential: 'ok' };
    app = mount(ScanReview, { target: host });
    await settle();

    [...host.querySelectorAll<HTMLButtonElement>('button')]
      .find((b) => b.textContent?.trim() === 'Match')!
      .click();
    flushSync();

    expect(document.querySelector('input[type="search"]')?.getAttribute('placeholder')).toBe(
      'Search TMDB for a series…',
    );
  });
});
