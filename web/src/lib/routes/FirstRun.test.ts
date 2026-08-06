/**
 * First run (SPEC §10.1, PLAN phase 10 tasks 1 and 6).
 *
 * The wizard is the one screen a user cannot come back to, so the tests are
 * about what it refuses to let them leave with: a key that does not work, or a
 * skip they did not make on purpose. And about the promise the whole adult
 * module rests on — an install that has never turned it on must not learn it
 * exists here.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import FirstRun from './FirstRun.svelte';
import { system } from '../state/system.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';

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
  counts: { movies: 0, series: 0, media_files: 3, unmatched: 1 },
  disk_free_bytes: 0,
  disk_total_bytes: 0,
  engine_health: 'ok',
  ffmpeg_available: false,
};

interface Call {
  url: string;
  method: string;
  body: Record<string, unknown> | null;
}

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: Call[];
/** Per-URL-fragment overrides, so one test can make the key test fail. */
let responders: { match: string; reply: () => Response }[];

beforeEach(() => {
  calls = [];
  responders = [];
  clearToasts();
  system.status = null;
  host = document.createElement('div');
  document.body.appendChild(host);

  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({
        url,
        method: init?.method ?? 'GET',
        body: typeof init?.body === 'string' ? JSON.parse(init.body) : null,
      });

      const override = responders.find((r) => url.includes(r.match));
      if (override) return override.reply();

      if (url.endsWith('/setup/admin')) return jsonResponse({ password_set: true }, 201);
      if (url.endsWith('/auth/me')) {
        return jsonResponse({ username: 'admin', role: 'admin', open: false, adult: true });
      }
      if (url.includes('/settings/metadata/test')) return jsonResponse({ status: 'ok' });
      if (url.includes('/storage-root/repoint')) {
        return jsonResponse({ root: '/data', warnings: [], restart_required: false });
      }
      if (url.endsWith('/settings')) return jsonResponse({});
      if (url.endsWith('/library/rescan')) return jsonResponse({ status: 'scanning' }, 202);
      if (url.endsWith('/system/status')) return jsonResponse(STATUS);
      throw new Error(`unexpected fetch: ${init?.method ?? 'GET'} ${url}`);
    }),
  );
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  system.status = null;
  vi.unstubAllGlobals();
});

async function settle() {
  for (let i = 0; i < 6; i += 1) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

function mountWizard() {
  app = mount(FirstRun, { target: host });
  flushSync();
}

function typeInto(selector: string, value: string): void {
  const field = host.querySelector(selector) as HTMLInputElement | null;
  expect(field, selector).not.toBeNull();
  field!.value = value;
  field!.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

function button(label: string): HTMLButtonElement {
  const found = [...host.querySelectorAll('button')].find((b) => b.textContent?.trim() === label);
  expect(found, `button labelled ${label}`).toBeDefined();
  return found as HTMLButtonElement;
}

function called(fragment: string): Call[] {
  return calls.filter((c) => c.url.includes(fragment));
}

/** Turn the scan step off when a test needs to prove no scan was queued. */
function scanOff() {
  const scanToggle = host.querySelector('[role="switch"]') as HTMLButtonElement;
  expect(scanToggle, 'the scan toggle').not.toBeNull();
  scanToggle.click();
  flushSync();
}
async function createAccount() {
  typeInto('#admin-username', 'admin');
  typeInto('#admin-password', 'correct horse');
  typeInto('#admin-confirm', 'correct horse');
  button('Create account').click();
  await settle();
}

describe('FirstRun', () => {
  it('offers the four setup steps on one screen', () => {
    mountWizard();

    expect(host.textContent).toContain('Create your administrator account');
    expect(host.textContent).toContain('Where does your media live?');
    expect(host.textContent).toContain('How should Caravan identify it?');
    expect(host.textContent).toContain('Already have a library?');
    expect(host.querySelector('#admin-username')).not.toBeNull();
    expect(host.querySelector('#storage-root')).not.toBeNull();
    expect(host.querySelector('#tmdb-key')).not.toBeNull();
  });

  it('resumes setup when system status says the administrator password is set', async () => {
    system.status = { ...STATUS, storage_root: '', needs_setup: true, password_set: true };
    mountWizard();

    expect(host.textContent).toContain('Administrator account created');
    expect(host.querySelector('#admin-username')).toBeNull();
    expect(called('/setup/admin')).toHaveLength(0);
    typeInto('#storage-root', '/data');

    scanOff();
    button('Skip for now').click();
    flushSync();
    button('Start Caravan').click();
    await settle();

    expect(called('/setup/admin')).toHaveLength(0);
    expect(called('/storage-root/repoint')).toHaveLength(1);
  });

  // PLAN phase 10 task 6, and the module's whole promise: an install that has
  // never turned adult content on must not learn it exists from the front door.
  it('contains zero adult-content references', () => {
    mountWizard();

    const text = (host.textContent ?? '').toLowerCase();
    for (const word of ['adult', 'stash', 'porn', 'scene', 'performer', 'nsfw']) {
      expect(text, `first run mentions "${word}"`).not.toContain(word);
    }
  });

  it('proves the key against TMDB before anything is written', async () => {
    mountWizard();
    typeInto('#tmdb-key', ' abc123 ');

    button('Test').click();
    await settle();

    // Trimmed, and sent in the body: the key is proved before it is stored, so
    // a wrong one never reaches the database.
    expect(called('/settings/metadata/test')).toEqual([
      expect.objectContaining({ method: 'POST', body: { api_key: 'abc123' } }),
    ]);
    expect(host.textContent).toContain('Key works');
    expect(called('/settings')).toHaveLength(1); // the test route only
  });

  it('saves the root and proven key, then goes to settings without a scan', async () => {
    mountWizard();
    await createAccount();
    typeInto('#storage-root', '/data');
    typeInto('#tmdb-key', 'abc123');
    scanOff();

    button('Test').click();
    await settle();
    button('Start Caravan').click();
    await settle();

    expect(called('/storage-root/repoint')).toHaveLength(1);
    // One test call, not two: the key was already proved, and the server holds
    // the verdict for that exact string.
    expect(called('/settings/metadata/test')).toHaveLength(1);
    expect(calls.find((c) => c.method === 'PUT' && c.url.endsWith('/settings'))?.body).toEqual({
      tmdb_api_key: 'abc123',
    });
    expect(called('/library/rescan')).toHaveLength(0);
    expect(window.location.pathname).toBe('/settings');
  });

  // The acceptance criterion: a wrong key is caught at first run, and it is
  // caught BEFORE the install is touched.
  it('refuses to finish with a key TMDB rejects, and writes nothing', async () => {
    responders.push({
      match: '/settings/metadata/test',
      reply: () =>
        jsonResponse(
          { error: 'metadata test failed: invalid api key', code: 'metadata_credential_invalid' },
          502,
        ),
    });
    mountWizard();
    await createAccount();
    typeInto('#storage-root', '/data');
    typeInto('#tmdb-key', 'wrong');
    scanOff();

    button('Start Caravan').click();
    await settle();

    expect(host.textContent).toContain('invalid api key');
    expect(called('/storage-root/repoint')).toHaveLength(0);
    expect(calls.some((c) => c.method === 'PUT')).toBe(false);
    // Still on the wizard, with the field that was wrong in front of them.
    expect(host.querySelector('#tmdb-key')).not.toBeNull();
  });

  // An untested key is not a reason to write a bad one: submitting proves it
  // first, and only a pass continues.
  it('tests an untested key on submit rather than saving it blind', async () => {
    mountWizard();
    await createAccount();
    typeInto('#storage-root', '/data');
    typeInto('#tmdb-key', 'abc123');
    scanOff();

    button('Start Caravan').click();
    await settle();

    expect(called('/settings/metadata/test')).toHaveLength(1);
    expect(called('/storage-root/repoint')).toHaveLength(1);
  });

  // The escape hatch, taken on purpose - and it names what it costs.
  it('lets the key be skipped, saying what that means, and saves no key', async () => {
    mountWizard();
    await createAccount();
    typeInto('#storage-root', '/data');
    scanOff();

    button('Skip for now').click();
    flushSync();
    expect(host.textContent).toContain('nothing is matched');
    expect(host.textContent).toContain('Settings → Metadata');

    button('Start Caravan').click();
    await settle();

    expect(called('/storage-root/repoint')).toHaveLength(1);
    expect(called('/settings/metadata/test')).toHaveLength(0);
    expect(calls.some((c) => c.method === 'PUT')).toBe(false);
    expect(called('/library/rescan')).toHaveLength(0);
    expect(window.location.pathname).toBe('/settings');
  });

  it('navigates to settings immediately after a scan queues', async () => {
    responders.push({
      match: '/system/status',
      reply: () => jsonResponse({ ...STATUS, scanning: true }),
    });
    mountWizard();
    await createAccount();
    typeInto('#storage-root', '/data');
    button('Skip for now').click();
    flushSync();

    button('Start Caravan').click();
    await settle();

    expect(called('/library/rescan')).toHaveLength(1);
    // Refresh observes the running scan once; waiting for completion would
    // keep the wizard here and poll status again after the scan interval.
    expect(called('/system/status')).toHaveLength(1);
    expect(window.location.pathname).toBe('/settings');
    expect(host.textContent).not.toContain('Starting…');
  });

  it('reports a scan start failure but still navigates to settings', async () => {
    responders.push({
      match: '/library/rescan',
      reply: () => jsonResponse({ error: 'scanner unavailable' }, 503),
    });
    mountWizard();
    await createAccount();
    typeInto('#storage-root', '/data');
    button('Skip for now').click();
    flushSync();

    button('Start Caravan').click();
    await settle();

    expect(called('/storage-root/repoint')).toHaveLength(1);
    expect(called('/library/rescan')).toHaveLength(1);
    expect(toasts.items).toContainEqual(
      expect.objectContaining({
        tone: 'warning',
        message: expect.stringContaining('could not start the scan: scanner unavailable'),
      }),
    );
    expect(window.location.pathname).toBe('/settings');
  });

  it('links the remaining setup directly to its settings', () => {
    mountWizard();

    expect(host.querySelector('a[href="/settings/indexers"]')?.textContent?.trim()).toBe('Indexers');
    expect(host.querySelector('a[href="/settings/downloads"]')?.textContent?.trim()).toBe('Downloads');
    expect(host.querySelector('a[href="/settings/quality-profiles"]')?.textContent?.trim()).toBe(
      'Download profiles',
    );
  });

  // "I have not typed it yet" is not the same answer as "I am going without
  // one", and only the second one should get through.
  it('still requires account creation when system status says no password is set', async () => {
    system.status = { ...STATUS, storage_root: '', needs_setup: true, password_set: false };
    mountWizard();
    expect(host.querySelector('#admin-username')).not.toBeNull();
    typeInto('#storage-root', '/data');
    button('Skip for now').click();
    flushSync();

    button('Start Caravan').click();
    await settle();

    expect(host.textContent).toContain('Create the administrator account');
    expect(called('/storage-root/repoint')).toHaveLength(0);
  });

  it('will not finish on an empty key that was never skipped', async () => {
    mountWizard();
    await createAccount();
    typeInto('#storage-root', '/data');
    scanOff();

    button('Start Caravan').click();
    await settle();

    expect(host.textContent).toContain('skip that step on purpose');
    expect(called('/storage-root/repoint')).toHaveLength(0);
  });

  it('re-arms the skip the moment a key is typed', async () => {
    mountWizard();
    button('Skip for now').click();
    flushSync();
    typeInto('#tmdb-key', 'abc123');

    // Typing is un-skipping: the escape hatch is offered again, and the key
    // now has to be proved.
    expect(host.textContent).not.toContain('nothing is matched');
    expect(button('Skip for now')).toBeDefined();
  });

  it('still refuses an empty storage root', async () => {
    mountWizard();
    button('Skip for now').click();
    flushSync();

    button('Start Caravan').click();
    await settle();

    expect(host.textContent).toContain('Enter the folder');
    expect(calls).toEqual([]);
  });
});
