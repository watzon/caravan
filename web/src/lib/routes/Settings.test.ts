import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Settings from './Settings.svelte';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const ENGINE_SETTINGS = {
  engine_listen_port: '51413',
  engine_max_connections: '80',
  engine_max_down_kbps: '1024',
  engine_max_up_kbps: '256',
  engine_seed_ratio: '2.5',
  engine_seed_days: '14',
};

const SYSTEM_STATUS = {
  version: 'test',
  mode: 'server',
  storage_root: '/data',
  schema_version: 4,
  scanning: false,
  counts: { movies: 0, series: 0, media_files: 0, unmatched: 0 },
  disk_free_bytes: 0,
  disk_total_bytes: 0,
  engine_health: 'ok',
};

let host: HTMLElement;
let app: Record<string, unknown>;

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  vi.useFakeTimers();
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

async function settle() {
  await vi.advanceTimersByTimeAsync(0);
  await Promise.resolve();
  flushSync();
}

function button(label: string) {
  const found = [...host.querySelectorAll('button')].find((candidate) => candidate.textContent?.includes(label));
  expect(found, `button labelled ${label}`).toBeDefined();
  return found!;
}

describe('Settings engine tab', () => {
  it('loads and saves all six engine settings', async () => {
    let savedPatch: Record<string, string> | null = null;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/settings') && (!init?.method || init.method === 'GET')) {
        return jsonResponse(ENGINE_SETTINGS);
      }
      if (url.endsWith('/settings') && init?.method === 'PUT') {
        savedPatch = JSON.parse(String(init.body));
        return jsonResponse({ ...ENGINE_SETTINGS, ...savedPatch });
      }
      if (url.endsWith('/system/status')) return jsonResponse(SYSTEM_STATUS);
      throw new Error(`unexpected fetch: ${url}`);
    }));

    app = mount(Settings, { target: host });
    await settle();
    button('Engine').click();
    flushSync();

    expect((host.querySelector('#engine-listen-port') as HTMLInputElement).value).toBe('51413');
    expect((host.querySelector('#engine-max-connections') as HTMLInputElement).value).toBe('80');
    expect((host.querySelector('#engine-seed-ratio') as HTMLInputElement).value).toBe('2.5');

    const down = host.querySelector('#engine-max-down-kbps') as HTMLInputElement;
    down.value = '2048';
    down.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    button('Save changes').click();
    await settle();

    expect(savedPatch).toEqual({
      engine_listen_port: '51413',
      engine_max_connections: '80',
      engine_max_down_kbps: '2048',
      engine_max_up_kbps: '256',
      engine_seed_ratio: '2.5',
      engine_seed_days: '14',
    });
  });
});
