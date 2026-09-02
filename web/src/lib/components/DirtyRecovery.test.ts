/**
 * Dirty-eject recovery (SPEC §2.3, §13): the banner that comes up after an
 * unclean shutdown, the fsck instructions it carries, and the verify action
 * that clears it.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import DirtyRecovery from './DirtyRecovery.svelte';
import { system } from '../state/system.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';
import type { SystemStatus } from '../api/types';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const STATUS: SystemStatus = {
  version: 'test',
  mode: 'portable',
  storage_root: '/Volumes/CARAVAN',
  schema_version: 5,
  scanning: false,
  counts: { movies: 0, series: 0, media_files: 0, unmatched: 0 },
  disk_free_bytes: 0,
  disk_total_bytes: 0,
  engine_health: 'ok',
  ffmpeg_available: true,
  dirty: true,
};

let host: HTMLElement;
let app: Record<string, unknown>;
let posted: string[] = [];
let verifyFails = false;

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  posted = [];
  verifyFails = false;
  system.status = { ...STATUS };
  clearToasts();

  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (method === 'POST') posted.push(url);
      if (url.endsWith('/system/verify')) {
        if (verifyFails) {
          return jsonResponse({ error: 'database integrity check failed' }, 500);
        }
        // Verifying is what makes the server report clean again.
        system.status = { ...STATUS, dirty: false };
        return jsonResponse({ integrity: 'ok', dirty: false, scanning: true });
      }
      if (url.endsWith('/system/status')) return jsonResponse(system.status);
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
  system.status = null;
  clearToasts();
});

async function settle() {
  for (let i = 0; i < 3; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

function button(label: string) {
  const found = [...host.querySelectorAll('button')].find((candidate) =>
    candidate.textContent?.includes(label),
  );
  expect(found, `button labelled ${label}`).toBeDefined();
  return found!;
}

describe('dirty-eject recovery', () => {
  it('explains the unclean shutdown and prints per-OS filesystem checks', () => {
    app = mount(DirtyRecovery, { target: host });
    flushSync();

    expect(host.textContent).toContain('Last shutdown was not clean');
    expect(host.textContent).toContain('Downloads stay paused');
    // Caravan never runs these itself; it names them for each platform.
    expect(host.textContent).toContain('diskutil verifyVolume');
    expect(host.textContent).toContain('fsck.exfat');
    expect(host.textContent).toContain('chkdsk');
  });

  it('verifies, refreshes status and reports that downloads can resume', async () => {
    app = mount(DirtyRecovery, { target: host });
    flushSync();

    button('Verify and rescan').click();
    await settle();

    expect(posted).toEqual(['/api/v1/system/verify']);
    expect(system.status?.dirty).toBe(false);
    expect(toasts.items.map((t) => t.message).join(' ')).toContain('Database verified');
  });

  it('keeps the banner and says why when the database fails its check', async () => {
    verifyFails = true;
    app = mount(DirtyRecovery, { target: host });
    flushSync();

    button('Verify and rescan').click();
    await settle();

    // The flag stays set: this is exactly the case downloads must not resume in.
    expect(system.status?.dirty).toBe(true);
    expect(host.textContent).toContain('Last shutdown was not clean');
    expect(toasts.items.map((t) => t.message).join(' ')).toContain('integrity check failed');
    expect(toasts.items.some((t) => t.tone === 'danger')).toBe(true);
  });
});
