/**
 * The routing pickers, mounted for real against a stubbed /api/v1.
 *
 * What is worth proving here is that the screen cannot express a routing that
 * would not route: only enabled clients of the right protocol are offered, and
 * each protocol's built-in engine is always available as its fallback. Leaving
 * both alone is a working Caravan, so the screen must never read as though an
 * external client were required.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import DownloadRouting from './DownloadRouting.svelte';
import type { DownloadClient, DownloadClientTypeInfo, Settings } from '../api/types';

const TYPES: DownloadClientTypeInfo[] = [
  {
    type: 'qbittorrent',
    label: 'qBittorrent',
    protocol: 'torrent',
    uses_login: true,
    uses_api_key: false,
    supported: true,
  },
  {
    type: 'sabnzbd',
    label: 'SABnzbd',
    protocol: 'usenet',
    uses_login: false,
    uses_api_key: true,
    supported: true,
  },
  {
    type: 'nzbget',
    label: 'NZBGet',
    protocol: 'usenet',
    uses_login: true,
    uses_api_key: false,
    supported: true,
  },
];

function client(over: Partial<DownloadClient> & Pick<DownloadClient, 'id' | 'type' | 'name'>) {
  return {
    url: 'http://127.0.0.1',
    username: '',
    has_password: false,
    has_api_key: false,
    category: '',
    priority: 25,
    enabled: true,
    ...over,
  } as DownloadClient;
}

const QBIT = client({ id: 7, type: 'qbittorrent', name: 'qBit' });
const SAB = client({ id: 9, type: 'sabnzbd', name: 'Sab' });
const NZBGET_OFF = client({ id: 11, type: 'nzbget', name: 'Old NZBGet', enabled: false });

type Call = { url: string; method: string; body: Record<string, unknown> | null };

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

let host: HTMLElement;
let app: Record<string, unknown>;
let calls: Call[];
let stored: Settings;

beforeEach(() => {
  calls = [];
  stored = {};
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      const body = typeof init?.body === 'string' ? JSON.parse(init.body) : null;
      calls.push({ url, method, body });
      if (url.endsWith('/settings')) {
        if (method === 'PUT') Object.assign(stored, body);
        return jsonResponse(stored);
      }
      throw new Error(`unexpected fetch: ${method} ${url}`);
    }),
  );
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
});

async function settle() {
  for (let i = 0; i < 3; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

async function render(clients: DownloadClient[], settings: Settings = {}) {
  stored = { ...settings };
  app = mount(DownloadRouting, { target: host, props: { clients, types: TYPES } });
  await settle();
}

function select(id: string): HTMLSelectElement {
  const el = host.querySelector<HTMLSelectElement>(`#${id}`);
  expect(el, `a select #${id}`).not.toBeNull();
  return el!;
}

function options(id: string): Array<{ value: string; label: string }> {
  return [...select(id).options].map((o) => ({ value: o.value, label: (o.textContent ?? '').trim() }));
}

function choose(id: string, value: string) {
  const el = select(id);
  el.value = value;
  el.dispatchEvent(new Event('change', { bubbles: true }));
  flushSync();
}

function clickButton(label: string) {
  const button = [...host.querySelectorAll('button')].find(
    (b) => (b.textContent ?? '').trim() === label,
  );
  expect(button, `a button labelled "${label}"`).toBeDefined();
  button!.click();
  flushSync();
}

function writeCalls() {
  return calls.filter((c) => c.method !== 'GET');
}

describe('DownloadRouting', () => {
  it('offers only enabled clients of the matching protocol', async () => {
    await render([QBIT, SAB, NZBGET_OFF]);

    expect(options('route-torrent')).toEqual([
      { value: 'embedded', label: 'Built-in engine' },
      { value: '7', label: 'qBit' },
    ]);
    // The disabled NZBGet is absent: its engine is not built, so picking it
    // would leave usenet unrouted without saying so.
    expect(options('route-usenet')).toEqual([
      { value: '', label: 'Built-in engine' },
      { value: '9', label: 'Sab' },
    ]);
  });

  it('always offers the built-in engine for torrents, with no client configured', async () => {
    await render([]);

    expect(options('route-torrent')).toEqual([{ value: 'embedded', label: 'Built-in engine' }]);
    expect(select('route-torrent').value).toBe('embedded');
    // Usenet has a built-in engine too, so a stock Caravan grabs both
    // protocols and the screen must not present a client as required
    // (PLAN phase 7 acceptance).
    expect(options('route-usenet')).toEqual([{ value: '', label: 'Built-in engine' }]);
    expect(select('route-usenet').value).toBe('');
    expect(host.textContent).not.toContain('No usenet client is configured');
    expect(host.textContent).toContain('Usenet servers');
  });

  it('loads the stored routing and saves both protocols', async () => {
    await render([QBIT, SAB], { route_torrent: '7', route_usenet: '9' });

    expect(select('route-torrent').value).toBe('7');
    expect(select('route-usenet').value).toBe('9');

    choose('route-torrent', 'embedded');
    clickButton('Save routing');
    await settle();

    expect(writeCalls()).toHaveLength(1);
    expect(writeCalls()[0]!.body).toEqual({ route_torrent: 'embedded', route_usenet: '9' });
  });

  it('does not save a route whose client is gone', async () => {
    // The stored usenet default names a client that has since been deleted.
    // It resolves to "nothing configured" at grab time, so the screen must not
    // hand it back as though it were in effect.
    await render([QBIT], { route_torrent: '7', route_usenet: '9' });

    expect(select('route-usenet').value).toBe('');

    clickButton('Save routing');
    await settle();

    expect(writeCalls()[0]!.body).toEqual({ route_torrent: '7', route_usenet: '' });
  });
});
