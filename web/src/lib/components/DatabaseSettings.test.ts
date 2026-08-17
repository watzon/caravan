import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import DatabaseSettings from './DatabaseSettings.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';

let host: HTMLElement;
let app: Record<string, unknown>;
let requests: { url: string; init?: RequestInit }[];

async function settle() {
  for (let i = 0; i < 3; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  requests = [];
  clearToasts();
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    requests.push({ url: String(input), init });
    return new Response(JSON.stringify({ restart_required: true }), {
      status: 202,
      headers: { 'Content-Type': 'application/json' },
    });
  }));
  app = mount(DatabaseSettings, { target: host });
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
});

describe('Database settings', () => {
  it('offers a same-origin backup download and explains its contents', () => {
    const link = host.querySelector('a[href="/api/v1/system/backup"]');
    expect(link?.textContent).toContain('Download backup');
    expect(host.textContent).toContain('Backups contain credentials');
  });

  it('confirms and stages a selected restore as raw SQLite', async () => {
    const input = host.querySelector('input[type="file"]') as HTMLInputElement;
    const file = new File(['SQLite format 3\0'], 'caravan.db', { type: 'application/vnd.sqlite3' });
    Object.defineProperty(input, 'files', { configurable: true, value: [file] });
    input.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();

    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('caravan.db');
    const stage = [...document.querySelectorAll('button')].find((button) => button.textContent?.includes('Stage restore'));
    expect(stage).toBeDefined();
    stage!.click();
    await settle();

    expect(requests).toHaveLength(1);
    expect(requests[0]?.url).toBe('/api/v1/system/restore');
    expect(requests[0]?.init?.method).toBe('POST');
    expect(requests[0]?.init?.body).toBe(file);
    expect((requests[0]?.init?.headers as Record<string, string>)['Content-Type']).toBe('application/vnd.sqlite3');
    expect(toasts.items.at(-1)?.message).toContain('Backup staged');
    expect(host.textContent).toContain('Restore ready');
  });

  it('accepts portable zip backups alongside SQLite types', () => {
    const input = host.querySelector('input[type="file"]') as HTMLInputElement;
    expect(input.accept).toContain('application/vnd.caravan.portable+zip');
    expect(input.accept).toContain('.zip');
    expect(input.accept).toContain('.caravan-backup');
    expect(input.accept).toContain('.db');
    expect(input.accept).toContain('application/vnd.sqlite3');
  });
});
