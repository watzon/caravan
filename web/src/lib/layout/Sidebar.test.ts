/**
 * The sidebar's credential warning rows.
 *
 * "The metadata provider" is a chain now, so the card carries one row per
 * credential that needs attention rather than the single TMDB row it started
 * as. These assert on rendered text on purpose: `npm run check` type-checks the
 * script blocks and not the templates, so a mistyped prop or a row that never
 * renders is a silent pass everywhere else.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount, unmount } from 'svelte';
import Sidebar from './Sidebar.svelte';
import type { SystemStatus } from '../api/types';
import { providers } from '../state/providers.svelte';
import { system } from '../state/system.svelte';

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

/** The credential fields under test, on an otherwise minimal admin status. */
function seed(fields: Partial<SystemStatus>): void {
  system.status = {
    version: '0.1.0',
    mode: 'server',
    storage_root: '/data',
    schema_version: 1,
    scanning: false,
    counts: { movies: 0, series: 0, media_files: 0, unmatched: 0 },
    disk_free_bytes: 0,
    disk_total_bytes: 0,
    engine_health: 'ok',
    ffmpeg_available: true,
    ...fields,
  } as SystemStatus;
  system.loading = false;
}

let host: HTMLElement;
let app: Record<string, unknown>;

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  window.scrollTo = () => {};
  // The shell polls the queue and the request badge as soon as it mounts.
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/downloads')) return jsonResponse({ downloads: [] });
      if (url.endsWith('/requests')) return jsonResponse({ requests: [] });
      if (url.endsWith('/system/status')) return jsonResponse(system.status ?? {});
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
  // Module singletons.
  system.status = null;
  system.loading = true;
  providers.all = [];
  providers.loaded = false;
});

/** The card's credential rows, which are the only links to the metadata screen. */
function credentialRows(): string[] {
  return [...host.querySelectorAll('a[href="/settings/metadata"]')].map(
    (a) => a.textContent?.trim() ?? '',
  );
}

function render(): void {
  app = mount(Sidebar, { target: host, props: { open: true, onclose: () => {} } });
}

describe('Sidebar credential rows', () => {
  it('warns once per credential that needs attention', () => {
    providers.all = [
      { id: 'tmdb', name: 'TMDB', kinds: ['movie'] },
      { id: 'thetvdb', name: 'TheTVDB', kinds: ['tv'] },
    ];
    seed({
      metadata_credential: 'invalid',
      metadata_credential_reason: 'tmdb: http 401: Invalid API key',
      metadata_credentials: {
        tmdb: { state: 'invalid', reason: 'tmdb: http 401: Invalid API key' },
        thetvdb: { state: 'absent' },
      },
    });
    render();

    expect(credentialRows()).toEqual(['No TheTVDB key', 'TMDB key rejected']);
    // The provider's own words, where they do not crowd the card.
    const rejected = [...host.querySelectorAll('a[href="/settings/metadata"]')].find((a) =>
      a.textContent?.includes('TMDB key rejected'),
    );
    expect(rejected?.getAttribute('title')).toContain('Invalid API key');
  });

  // The provider list is admin-only and loaded lazily by the surfaces that need
  // names, so the shell usually has none. A row named by its id is a worse
  // label but never a wrong one — except TMDB's, whose row predates the list.
  it('degrades an unnamed provider to its id and still brands TMDB', () => {
    seed({
      metadata_credential: 'absent',
      metadata_credentials: {
        tmdb: { state: 'absent' },
        thetvdb: { state: 'absent' },
      },
    });
    render();

    expect(credentialRows()).toEqual(['No thetvdb key', 'No TMDB key']);
  });

  it('says nothing while every credential works', () => {
    seed({
      metadata_credential: 'ok',
      metadata_credentials: { tmdb: { state: 'ok' }, thetvdb: { state: 'ok' } },
    });
    render();

    expect(credentialRows()).toEqual([]);
    expect(host.textContent).not.toContain('key rejected');
  });

  // An older server sends no map at all, and the row it has always raised must
  // still be raised from the flat fields.
  it('still warns about TMDB from the flat fields alone', () => {
    seed({ metadata_credential: 'invalid', metadata_credential_reason: 'tmdb: http 401' });
    render();

    expect(credentialRows()).toEqual(['TMDB key rejected']);
  });
});
