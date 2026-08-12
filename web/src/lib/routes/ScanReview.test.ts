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
import { session } from '../state/session.svelte';
import type { Library, SceneMeta, SystemStatus, UnmatchedFile } from '../api/types';

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
let scenes: SceneMeta[];
let failAdultDiscover: ((provider: string, page: number) => boolean) | null;
let scenePageSize: number | null;
let sceneSearchURLs: string[];
let siteSearchURLs: string[];
let performerSearchURLs: string[];
let holdThinPerformerSearch: boolean;
let thinPerformerSearchAborted: boolean;
let matchBodies: unknown[];

beforeEach(() => {
  unmatched = [];
  failAdultDiscover = null;
  scenePageSize = null;
  scenes = [];
  sceneSearchURLs = [];
  siteSearchURLs = [];
  performerSearchURLs = [];
  holdThinPerformerSearch = false;
  thinPerformerSearchAborted = false;
  matchBodies = [];
  host = document.createElement('div');
  document.body.appendChild(host);
  system.status = null;
  session.user = null;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url.includes('/import/queue/') && url.endsWith('/match') && method === 'POST') {
        matchBodies.push(JSON.parse(String(init?.body ?? '{}')));
        return jsonResponse({ status: 'matched' });
      }
      if (url.includes('/adult/stashbox-instances')) {
        const thin = {
          year: false,
          duration: false,
          site_scope: false,
          date_op: false,
          sort_duration: false,
          sort_relevance: false,
          any_of: false,
        };
        const full = {
          year: true,
          duration: true,
          site_scope: true,
          date_op: true,
          sort_duration: true,
          sort_relevance: true,
          any_of: true,
        };
        return jsonResponse({
          instances: [
            {
              id: 1,
              provider_id: 'stashbox',
              name: 'StashDB',
              endpoint: 'https://stashdb.org/graphql',
              has_api_key: true,
              scene_filters: thin,
              library_count: 1,
              item_count: 0,
            },
            {
              id: 2,
              provider_id: 'stashbox:theporndb',
              name: 'ThePornDB',
              endpoint: 'https://theporndb.net/graphql',
              has_api_key: true,
              scene_filters: full,
              library_count: 1,
              item_count: 0,
            },
          ],
        });
      }
      if (url.includes('/adult/search')) {
        siteSearchURLs.push(url);
        const provider = new URL(url, 'http://caravan.test').searchParams.get('provider')
          ?? 'stashbox';
        return jsonResponse({
          sites: [{
            provider,
            stash_id: 'site-african-casting',
            name: 'African Casting',
            aliases: ['AfricanCasting'],
            parent_name: '',
            url: '',
            image_url: '',
            in_library: true,
            library_id: 3,
          }],
        });
      }
      if (url.includes('/adult/performers')) {
        performerSearchURLs.push(url);
        const provider = new URL(url, 'http://caravan.test').searchParams.get('provider')
          ?? 'stashbox';
        if (holdThinPerformerSearch && provider === 'stashbox') {
          return new Promise<Response>((_resolve, reject) => {
            const abort = () => {
              thinPerformerSearchAborted = true;
              reject(new DOMException('Aborted', 'AbortError'));
            };
            if (init?.signal?.aborted) abort();
            else init?.signal?.addEventListener('abort', abort, { once: true });
          });
        }
        return jsonResponse({
          performers: [{
            id: provider === 'stashbox:theporndb' ? 'performer-tpdb' : 'performer-stashdb',
            name: provider === 'stashbox:theporndb' ? 'TPDB Performer' : 'StashDB Performer',
            image_url: '',
          }],
        });
      }
      if (url.includes('/adult/tags')) return jsonResponse({ tags: [] });
      if (url.includes('/adult/discover')) {
        sceneSearchURLs.push(url);
        const query = new URL(url, 'http://caravan.test').searchParams;
        const provider = query.get('provider') ?? 'stashbox';
        const date = query.get('date');
        const providerScenes = scenes
          .filter((scene) => !date || scene.date === date)
          .map((scene) => ({ ...scene, provider }));
        const page = Number(query.get('page') ?? 1);
        if (failAdultDiscover?.(provider, page)) {
          return jsonResponse({ error: 'provider page failed' }, 502);
        }
        const perPage = scenePageSize ?? providerScenes.length;
        const pageScenes = scenePageSize === null
          ? providerScenes
          : providerScenes.slice((page - 1) * perPage, page * perPage);
        return jsonResponse({
          page,
          per_page: perPage,
          total: providerScenes.length,
          scenes: pageScenes,
        });
      }
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
  session.user = null;
  vi.useRealTimers();
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
    active: true,
    restricted: false,
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

  it('keeps the full unmatched path available for responsive overflow', async () => {
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
    expect(pathCell?.textContent?.trim()).toBe(path);
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
      'Search TMDB for a series...',
    );
  });

  it('lets an adult match correct provider filters before selecting the exact scene', async () => {
    libraries.all = [library({
      id: 3,
      kind: 'adult',
      name: 'Adult',
      root_path: 'library/Adult',
      provider: 'stashbox',
      providers: ['stashbox', 'stashbox:theporndb'],
      restricted: true,
    })];
    libraries.loaded = true;
    session.user = {
      username: 'admin',
      role: 'admin',
      open: false,
      adult: true,
      scene_filters: {
        year: false,
        duration: false,
        site_scope: false,
        date_op: false,
        sort_duration: false,
        sort_relevance: false,
        any_of: false,
      },
    };
    unmatched = [parkedFile({
      path: 'incomplete/AfricanCasting.20.01.26.Scarlet.XXX.1080p.MP4-WRB/hash.mp4',
      library_id: 3,
      parsed: {
        ...parkedFile().parsed,
        title: 'AfricanCasting',
        year: 2020,
        scene_date: '2020-01-26T00:00:00Z',
      },
    })];
    scenes = [{
      media_type: 'scene',
      provider: 'stashbox:theporndb',
      stash_id: 'scene-scarlet',
      site_stash_id: 'site-african-casting',
      site_name: 'African Casting',
      title: 'Scarlet Came Back for More',
      code: '',
      overview: '',
      date: '2020-09-16',
      duration: 1800,
      performers: ['Scarlet'],
      url: '',
      image_url: '',
      in_library: false,
      library_id: 0,
      requested: false,
    }];
    system.status = { ...STATUS, metadata_credential: 'ok' };
    app = mount(ScanReview, { target: host });
    await settle();

    vi.useFakeTimers();
    [...host.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent?.trim() === 'Match')!
      .click();
    flushSync();
    await vi.advanceTimersByTimeAsync(500);
    flushSync();
    vi.useRealTimers();
    await settle();

    const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!;
    const queryInput = dialog.querySelector<HTMLInputElement>('#scene-picker-query')!;
    const siteInput = dialog.querySelector<HTMLInputElement>('#scene-picker-site')!;
    const dateInput = dialog.querySelector<HTMLInputElement>('#scene-picker-date')!;
    const providerSelect = dialog.querySelector<HTMLSelectElement>('#scene-picker-provider')!;
    expect(queryInput.value).toBe('Scarlet');
    expect(siteInput.value).toBe('AfricanCasting');
    expect(dateInput.value).toBe('2020-01-26');
    expect(providerSelect.value).toBe('stashbox');
    expect(dialog.querySelector('#scene-picker-year')).toBeNull();
    expect(dialog.textContent).not.toContain('Scarlet Came Back for More');

    const initialSiteQuery = new URL(siteSearchURLs.at(-1)!, 'http://caravan.test').searchParams;
    expect(initialSiteQuery.get('provider')).toBe('stashbox');
    expect(initialSiteQuery.get('q')).toBe('AfricanCasting');
    const initialSceneQuery = new URL(sceneSearchURLs.at(-1)!, 'http://caravan.test').searchParams;
    expect(initialSceneQuery.get('provider')).toBe('stashbox');
    expect(initialSceneQuery.get('site')).toBe('site-african-casting');
    expect(initialSceneQuery.get('date')).toBe('2020-01-26');
    expect(initialSceneQuery.get('date_op')).toBe('on');
    expect(initialSceneQuery.has('year')).toBe(false);

    holdThinPerformerSearch = true;
    vi.useFakeTimers();
    const thinPerformerInput = dialog.querySelector<HTMLInputElement>(
      'input[aria-label="Search performers on the selected provider"]',
    )!;
    thinPerformerInput.value = 'Scar';
    thinPerformerInput.dispatchEvent(new Event('input', { bubbles: true }));
    await vi.advanceTimersByTimeAsync(250);
    flushSync();
    expect(
      new URL(performerSearchURLs.at(-1)!, 'http://caravan.test').searchParams.get('provider'),
    ).toBe('stashbox');

    providerSelect.value = 'stashbox:theporndb';
    providerSelect.dispatchEvent(new Event('change', { bubbles: true }));
    dateInput.value = '2020-09-16';
    dateInput.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    expect(thinPerformerSearchAborted).toBe(true);
    expect(dialog.querySelector('#scene-picker-year')).not.toBeNull();
    expect(dialog.querySelector('#scene-picker-date-op')).not.toBeNull();

    const tpdbPerformerInput = dialog.querySelector<HTMLInputElement>(
      'input[aria-label="Search performers on the selected provider"]',
    )!;
    expect(tpdbPerformerInput).not.toBe(thinPerformerInput);
    expect(tpdbPerformerInput.value).toBe('');
    tpdbPerformerInput.value = 'Scar';
    tpdbPerformerInput.dispatchEvent(new Event('input', { bubbles: true }));
    await vi.advanceTimersByTimeAsync(500);
    flushSync();
    [...dialog.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent?.trim() === 'TPDB Performer')!
      .click();
    flushSync();
    await vi.advanceTimersByTimeAsync(250);
    flushSync();
    vi.useRealTimers();
    await settle();

    expect(
      new URL(performerSearchURLs.at(-1)!, 'http://caravan.test').searchParams.get('provider'),
    ).toBe('stashbox:theporndb');
    const correctedSceneQuery = new URL(sceneSearchURLs.at(-1)!, 'http://caravan.test').searchParams;
    expect(correctedSceneQuery.get('provider')).toBe('stashbox:theporndb');
    expect(correctedSceneQuery.get('q')).toBe('Scarlet');
    expect(correctedSceneQuery.get('site')).toBe('site-african-casting');
    expect(correctedSceneQuery.get('date')).toBe('2020-09-16');
    expect(correctedSceneQuery.get('date_op')).toBe('on');
    expect(correctedSceneQuery.get('performers')).toBe('performer-tpdb:TPDB Performer');
    expect(correctedSceneQuery.get('performers_all')).toBe('true');
    expect(dialog.textContent).toContain('Scarlet Came Back for More');

    [...dialog.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent?.trim() === 'Match')!
      .click();
    await settle();

    expect(matchBodies).toEqual([{
      type: 'scene',
      provider: 'stashbox:theporndb',
      provider_ref: 'scene-scarlet',
    }]);
    expect(host.textContent).toContain('Nothing to review');
  });
});
