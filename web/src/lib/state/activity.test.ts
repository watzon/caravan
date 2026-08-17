import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { MediaRequest, SystemStatus } from '../api/types';
import { libraryChanged, requestCreated, requestDecided, scanReviewResolved, searchQueued } from './activity';
import { downloads } from './downloads.svelte';
import { requests } from './requests.svelte';
import { system } from './system.svelte';
import { tasks } from './tasks.svelte';

function request(extra: Partial<MediaRequest> = {}): MediaRequest {
  return {
    id: 7,
    media_type: 'scene',
    tmdb_id: 0,
    stash_id: 'abc',
    title: 'Deep Impact',
    year: 2022,
    poster_path: '',
    poster_url: '',
    seasons: null,
    min_availability: '',
    requested_by_username: '',
    status: 'pending',
    created_at: '',
    updated_at: '',
    ...extra,
  };
}

beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/downloads')) {
        return new Response(JSON.stringify({ downloads: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (url.includes('/system/status')) {
        return new Response(
          JSON.stringify({
            version: '0',
            mode: 'server',
            storage_root: '/data',
            schema_version: 1,
            scanning: false,
            counts: { movies: 0, series: 0, media_files: 0, unmatched: 0, wanted: 1 },
            disk_free_bytes: 0,
            disk_total_bytes: 0,
            engine_health: 'ok',
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        );
      }
      if (url.includes('/system/tasks')) {
        return new Response(JSON.stringify({ tasks: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (url.includes('/jobs')) {
        return new Response(JSON.stringify({ jobs: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
});

afterEach(() => {
  downloads.stopSoon();
  tasks.stopSoon();
  tasks.tasks = null;
  tasks.jobs = null;
  requests.items = null;
  system.status = null;
  vi.unstubAllGlobals();
});

describe('activity', () => {
  it('puts a created request on the badge immediately', () => {
    requests.items = [];
    requestCreated(request());
    expect(requests.pendingCount).toBe(1);
  });

  it('drops an approved request from the badge and bumps Wanted', () => {
    requests.items = [request()];
    system.status = {
      version: '0',
      mode: 'server',
      storage_root: '/data',
      schema_version: 1,
      scanning: false,
      counts: { movies: 0, series: 0, media_files: 0, unmatched: 0, wanted: 2 },
      disk_free_bytes: 0,
      disk_total_bytes: 0,
      engine_health: 'ok',
      ffmpeg_available: true,
    } as SystemStatus;
    requestDecided(7, 'approved', { expectDownload: true });
    expect(requests.pendingCount).toBe(0);
    expect(requests.items?.[0]?.status).toBe('approved');
    expect(system.status?.counts.wanted).toBe(3);
  });

  it('refreshes inventory after a library add', () => {
    libraryChanged({ expectDownload: true });
    expect(vi.mocked(fetch).mock.calls.some((call) => String(call[0]).includes('/system/status'))).toBe(
      true,
    );
  });

  it('watches the task rail when a search is queued', () => {
    searchQueued(2);
    expect(vi.mocked(fetch).mock.calls.some((call) => String(call[0]).includes('/system/tasks'))).toBe(
      true,
    );
    expect(vi.mocked(fetch).mock.calls.some((call) => String(call[0]).includes('/jobs'))).toBe(true);
  });

  it('ignores a zero-queue search', () => {
    searchQueued(0);
    expect(vi.mocked(fetch).mock.calls.some((call) => String(call[0]).includes('/system/tasks'))).toBe(
      false,
    );
  });

  it('drops the unmatched badge as soon as a scan-review row is resolved', () => {
    system.status = {
      version: '0',
      mode: 'server',
      storage_root: '/data',
      schema_version: 1,
      scanning: false,
      counts: { movies: 0, series: 0, media_files: 0, unmatched: 4, wanted: 1 },
      disk_free_bytes: 0,
      disk_total_bytes: 0,
      engine_health: 'ok',
      ffmpeg_available: true,
    } as SystemStatus;
    scanReviewResolved();
    expect(system.status?.counts.unmatched).toBe(3);
    expect(vi.mocked(fetch).mock.calls.some((call) => String(call[0]).includes('/system/status'))).toBe(
      true,
    );
  });
});
