/**
 * The Storage settings section (SPEC §10, PLAN phase 5 task 4).
 *
 * The two operations are deliberately not interchangeable, and these tests say
 * so: re-point posts one root and never touches media; migrate queues a job and
 * the screen then polls a row through running, done and rolled-back.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Settings from '../routes/Settings.svelte';
import { system } from '../state/system.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';
import type { StorageMigration, StorageMigrationStatus, SystemStatus } from '../api/types';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const STATUS: SystemStatus = {
  version: 'test',
  mode: 'server',
  storage_root: '/old-root',
  schema_version: 6,
  scanning: false,
  counts: { movies: 0, series: 0, media_files: 0, unmatched: 0 },
  disk_free_bytes: 0,
  disk_total_bytes: 0,
  engine_health: 'ok',
  ffmpeg_available: true,
  password_set: true,
  listening_publicly: false,
};

function migrationRow(over: Partial<StorageMigration> = {}): StorageMigration {
  return {
    id: 1,
    source_root: '/old-root',
    target_root: '/new-root',
    status: 'running',
    files_total: 4,
    files_done: 1,
    bytes_total: 4000,
    bytes_done: 1000,
    error: '',
    created_at: '2026-08-01T00:00:00Z',
    updated_at: '2026-08-01T00:00:10Z',
    ...over,
  };
}

let host: HTMLElement;
let app: Record<string, unknown>;
let posted: { url: string; body: unknown }[] = [];
let migration: StorageMigrationStatus = { migration: null, restart_required: false };
let repointReply: unknown = { root: '/new-root', warnings: [], restart_required: false };
let repointStatus = 200;
let storageRoot = '/old-root';
let mode: SystemStatus['mode'] = 'server';

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  posted = [];
  migration = { migration: null, restart_required: false };
  repointReply = { root: '/new-root', warnings: [], restart_required: false };
  repointStatus = 200;
  storageRoot = '/old-root';
  mode = 'server';
  system.status = null;
  clearToasts();

  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (method === 'POST') {
        posted.push({ url, body: init?.body ? JSON.parse(String(init.body)) : null });
      }
      if (url.endsWith('/system/storage-root/repoint')) {
        if (repointStatus !== 200) return jsonResponse(repointReply, repointStatus);
        storageRoot = (JSON.parse(String(init?.body)) as { root: string }).root;
        return jsonResponse(repointReply);
      }
      if (url.endsWith('/system/storage-root/migrate')) {
        migration = { migration: migrationRow({ status: 'queued' }), restart_required: false };
        return jsonResponse(migration.migration, 202);
      }
      if (url.endsWith('/system/storage-root/migration')) return jsonResponse(migration);
      if (url.endsWith('/settings')) return jsonResponse({ storage_root: storageRoot });
      if (url.endsWith('/system/status')) return jsonResponse({ ...STATUS, mode, storage_root: storageRoot });
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
  system.status = null;
});

async function settle() {
  for (let i = 0; i < 3; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

function button(label: string, scope: ParentNode = host) {
  const found = [...scope.querySelectorAll('button')].find((candidate) =>
    candidate.textContent?.includes(label),
  );
  expect(found, `button labelled ${label}`).toBeDefined();
  return found!;
}

// The confirmation dialogs repeat the labels of the buttons that open them, so
// the footer button has to be found inside the dialog rather than on the page.
function confirmButton(label: string) {
  const dialog = document.querySelector('[role="dialog"]');
  expect(dialog, 'an open confirmation dialog').not.toBeNull();
  return button(label, dialog!.querySelector('footer')!);
}

function type(selector: string, value: string) {
  const field = host.querySelector(selector) as HTMLInputElement;
  expect(field, selector).not.toBeNull();
  field.value = value;
  field.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

async function openStorageTab() {
  await system.refresh();
  app = mount(Settings, { target: host });
  await settle();
  button('Storage').click();
  flushSync();
  await settle();
}

describe('Storage settings', () => {
  it('shows the current root and refuses to act until a different one is typed', async () => {
    await openStorageTab();

    expect((host.querySelector('#settings-storage-root-current') as HTMLInputElement).value).toBe(
      '/old-root',
    );
    expect(button('Re-point').hasAttribute('disabled')).toBe(true);

    // Re-pointing at the root already in force is not an operation.
    type('#settings-storage-root', '/old-root');
    expect(button('Re-point').hasAttribute('disabled')).toBe(true);

    type('#settings-storage-root', '/new-root');
    expect(button('Re-point').hasAttribute('disabled')).toBe(false);
    expect(button('Move files here').hasAttribute('disabled')).toBe(false);
  });

  it('re-points through its own endpoint after a confirmation, and surfaces warnings', async () => {
    repointReply = {
      root: '/new-root',
      warnings: ['the library folder here is empty'],
      restart_required: true,
    };
    await openStorageTab();

    type('#settings-storage-root', '/new-root');
    button('Re-point').click();
    flushSync();
    // The confirmation is not ceremony: re-pointing at the wrong folder reads
    // as a library that vanished.
    expect(host.textContent).toContain('No files are moved');

    confirmButton('Re-point').click();
    await settle();

    expect(posted.map((p) => p.url)).toContain('/api/v1/system/storage-root/repoint');
    expect(posted.find((p) => p.url.endsWith('/repoint'))?.body).toEqual({ root: '/new-root' });
    // A warning is shown but the operation happened anyway.
    expect(host.textContent).toContain('the library folder here is empty');
    expect(host.textContent).toContain('Restart to move the download queue');
    expect((host.querySelector('#settings-storage-root-current') as HTMLInputElement).value).toBe(
      '/new-root',
    );
  });

  it('reports a refused re-point without changing the root on screen', async () => {
    repointStatus = 400;
    repointReply = { error: '/nope does not exist' };
    await openStorageTab();

    type('#settings-storage-root', '/nope');
    button('Re-point').click();
    flushSync();
    confirmButton('Re-point').click();
    await settle();

    expect((host.querySelector('#settings-storage-root-current') as HTMLInputElement).value).toBe(
      '/old-root',
    );
    // The refusal reaches the user as a toast; the shell owns the toast host,
    // so the assertion is on the queue rather than on this component's DOM.
    expect(toasts.items.map((t) => t.message)).toContain('/nope does not exist');
  });

  it('queues a migration and polls it through to done', async () => {
    await openStorageTab();

    type('#settings-storage-root', '/new-root');
    button('Move files here').click();
    flushSync();
    expect(host.textContent).toContain('Downloads pause for the duration');

    confirmButton('Move files').click();
    await settle();

    expect(posted.find((p) => p.url.endsWith('/migrate'))?.body).toEqual({ root: '/new-root' });
    expect(host.textContent).toContain('/old-root → /new-root');

    // Poll once by remounting the tab rather than waiting on the interval.
    migration = { migration: migrationRow({ status: 'running' }), restart_required: false };
    await openStorageTabAgain();
    expect(host.textContent).toContain('Moving files');
    expect(host.textContent).toContain('1 / 4 files');

    storageRoot = '/new-root';
    migration = {
      migration: migrationRow({ status: 'done', files_done: 4, bytes_done: 4000 }),
      restart_required: true,
    };
    await openStorageTabAgain();
    expect(host.textContent).toContain('Migration finished');
    expect(host.textContent).toContain('Restart to move the download queue');
  });

  it('renders the rolled-back terminal state as recoverable, and failure as not', async () => {
    migration = {
      migration: migrationRow({ status: 'rolled_back', error: 'the target filled up.' }),
      restart_required: false,
    };
    await openStorageTab();
    expect(host.textContent).toContain('Migration rolled back');
    expect(host.textContent).toContain('the target filled up.');
    expect(host.textContent).toContain('put back under /old-root');

    migration = {
      migration: migrationRow({ status: 'failed', error: '3 files are stranded.' }),
      restart_required: false,
    };
    await openStorageTabAgain();
    expect(host.textContent).toContain('could not be undone');
    expect(host.textContent).toContain('Part of the library is under each root');
  });

  // A prepared drive's storage root is the literal "." — that is what makes it
  // work on whatever machine it is plugged into, and cmd/caravan/prepare_test.go
  // asserts exactly that value. Neither operation on this screen can honour it:
  // "Move files here" returned 400 "the current storage root is not an absolute
  // path" every single time, and "Re-point" only accepts an absolute path, which
  // silently converts the drive into a one-machine install.
  it('offers neither operation on a portable drive, and says why', async () => {
    mode = 'portable';
    storageRoot = '.';
    await openStorageTab();

    expect((host.querySelector('#settings-storage-root-current') as HTMLInputElement).value).toBe('.');
    expect(host.querySelector('#settings-storage-root')).toBeNull();
    expect([...host.querySelectorAll('button')].map((b) => b.textContent?.trim())).not.toContain(
      'Re-point',
    );
    expect(host.textContent).toContain('portable drive');
    // The rescan is still offered: it is the one storage action that is safe
    // and useful on a drive.
    expect(button('Rescan library')).toBeDefined();
  });
});

// openStorageTabAgain remounts the section so its onMount poll runs, which is
// how these tests advance the migration without leaning on the 2s interval.
async function openStorageTabAgain() {
  unmount(app);
  await system.refresh();
  app = mount(Settings, { target: host });
  await settle();
  button('Storage').click();
  flushSync();
  await settle();
}
