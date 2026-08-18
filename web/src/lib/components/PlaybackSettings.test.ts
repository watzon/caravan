/**
 * Settings → Playback, tested for one thing only: whether the Stash card is
 * there (PLAN phase 11 task 5).
 *
 * The other three cards have their own files. This one exists because the gate
 * lives here rather than inside StashSettings, and a gate nobody mounts the
 * pane to check is a gate that quietly stops working. "Not rendered" is not
 * enough either — an ungranted browser must not put GET /adult/stash on the
 * wire at all, so the fetch log is asserted alongside the markup.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import PlaybackSettings from './PlaybackSettings.svelte';
import type { DlnaStatus, Settings, StashConfig } from '../api/types';
import { session } from '../state/session.svelte';

const DLNA: DlnaStatus = {
  enabled: true,
  friendly_name: 'Caravan',
  uuid: '1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed',
  advertising: true,
  error: '',
};

const STASH: StashConfig = { url: 'http://stash.lan:9999', api_key: 'k', enabled: true };

const SETTINGS: Settings = {};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

let host: HTMLElement;
let app: Record<string, unknown>;
let fetched: string[];

beforeEach(() => {
  fetched = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      fetched.push(url);
      if (url.endsWith('/dlna')) return jsonResponse(DLNA);
      if (url.endsWith('/libraries')) return jsonResponse({ libraries: [] });
      if (url.endsWith('/handoff/jellyfin')) return jsonResponse({ url: '', api_key: '', enabled: false });
      if (url.endsWith('/adult/stash')) return jsonResponse(STASH);
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
  host = document.createElement('div');
  vi.useFakeTimers();
  document.body.appendChild(host);
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
  // A module singleton: the next file must not inherit this identity.
  session.forget();
});

async function settle() {
  await vi.advanceTimersByTimeAsync(0);
  await Promise.resolve();
  await vi.advanceTimersByTimeAsync(0);
  await Promise.resolve();
  flushSync();
}

async function mountPane() {
  app = mount(PlaybackSettings, {
    target: host,
    props: { settings: SETTINGS, saving: false, onsave: async () => true },
  });
  await settle();
}

describe('PlaybackSettings', () => {
  it('shows the Stash card when the adult module is visible to this caller', async () => {
    session.user = { username: 'root', role: 'admin', open: false, adult: true };
    await mountPane();

    expect(host.querySelector('#stash-url')).not.toBeNull();
    expect(fetched.some((url) => url.endsWith('/adult/stash'))).toBe(true);
    // The Jellyfin card is unconditional and still there beside it.
    expect(host.querySelector('#jellyfin-url')).not.toBeNull();
  });

  it('hides the Stash card, and asks for nothing, without adult visibility', async () => {
    session.user = { username: 'ada', role: 'member', open: false, adult: false };
    await mountPane();

    expect(host.querySelector('#stash-url')).toBeNull();
    expect(host.textContent).not.toContain('Stash');
    expect(fetched.some((url) => url.includes('/adult/'))).toBe(false);
    // The rest of the pane is untouched — hiding one card is not hiding four.
    expect(host.querySelector('#jellyfin-url')).not.toBeNull();
  });

  it('hides the Stash card while nobody is known yet', async () => {
    // `session.adult` reads false before /auth/me answers, which is the whole
    // reason it is a separate rule from `session.isAdmin` (see adult.ts).
    session.forget();
    await mountPane();

    expect(host.querySelector('#stash-url')).toBeNull();
    expect(fetched.some((url) => url.includes('/adult/'))).toBe(false);
  });
});
