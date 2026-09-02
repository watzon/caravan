/**
 * First run (SPEC §10.1).
 *
 * The wizard is the one flow a user cannot come back to, so the tests are about
 * what it refuses to let them leave with: a key that does not work. Leaving a
 * metadata field blank is how you skip it. And about the promise the whole
 * adult module rests on — an install that has never turned it on must not learn
 * it exists here.
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

function testButton(which: 'tmdb' | 'thetvdb'): HTMLButtonElement {
  const tests = [...host.querySelectorAll('button')].filter((b) => b.textContent?.trim() === 'Test');
  const found = tests[which === 'tmdb' ? 0 : 1];
  expect(found, `${which} Test`).toBeDefined();
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

function openPin() {
  const details = host.querySelector('details');
  expect(details, 'the subscriber PIN accordion').not.toBeNull();
  details!.open = true;
  details!.dispatchEvent(new Event('toggle'));
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
  it('creates the administrator on the first screen before showing configuration', async () => {
    mountWizard();

    expect(host.textContent).toContain('Create your administrator account');
    expect(host.querySelector('[aria-current="step"]')?.textContent).toContain('Account');
    expect(host.querySelector('#admin-username')).not.toBeNull();
    expect(host.querySelector('#storage-root')).toBeNull();
    expect(host.querySelector('#tmdb-key')).toBeNull();
    expect(host.querySelector('#thetvdb-key')).toBeNull();

    await createAccount();

    expect(called('/setup/admin')).toHaveLength(1);
    expect(host.querySelector('[aria-current="step"]')?.textContent).toContain('Configuration');
    expect(host.querySelector('#admin-username')).toBeNull();
    expect(host.textContent).toContain('Where does your media live?');
    expect(host.textContent).toContain('How should Caravan identify it?');
    expect(host.textContent).toContain('Already have a library?');
    expect(host.querySelector('#storage-root')).not.toBeNull();
    expect(host.querySelector('#tmdb-key')).not.toBeNull();
    expect(host.querySelector('#thetvdb-key')).not.toBeNull();
    expect(host.querySelector('details')?.open).toBe(false);
    expect(host.textContent).not.toContain('Skip for now');
  });

  it('associates setup inputs with visible labels and names the scan switch', async () => {
    mountWizard();

    for (const [id, label] of [
      ['admin-username', 'Username'],
      ['admin-password', 'Password'],
      ['admin-confirm', 'Confirm password'],
    ]) {
      expect(host.querySelector(`label[for="${id}"]`)?.textContent).toContain(label);
    }

    await createAccount();

    for (const [id, label] of [
      ['storage-root', 'Storage root'],
      ['tmdb-key', 'TMDB API key'],
      ['thetvdb-key', 'TheTVDB API key'],
    ]) {
      expect(host.querySelector(`label[for="${id}"]`)?.textContent).toContain(label);
    }
    expect(host.querySelector('details summary')?.textContent).toContain('Subscriber PIN');
    openPin();
    expect(host.querySelector('label[for="thetvdb-pin"]')?.textContent).toContain('Subscriber PIN');
    expect(host.querySelector('[role="switch"]')?.getAttribute('aria-label')).toBe(
      'Scan for existing media now',
    );
  });

  it('resumes setup when system status says the administrator password is set', async () => {
    system.status = { ...STATUS, storage_root: '', needs_setup: true, password_set: true };
    mountWizard();

    expect(host.textContent).toContain('Administrator account created');
    expect(host.querySelector('#admin-username')).toBeNull();
    expect(called('/setup/admin')).toHaveLength(0);
    typeInto('#storage-root', '/data');

    scanOff();
    button('Start Caravan').click();
    await settle();

    expect(called('/setup/admin')).toHaveLength(0);
    expect(called('/storage-root/repoint')).toHaveLength(1);
  });

  // The module's whole promise: an install that has never turned adult content
  // on must not learn it exists from the front door.
  it('contains zero adult-content references', () => {
    mountWizard();

    const text = (host.textContent ?? '').toLowerCase();
    for (const word of ['adult', 'stash', 'porn', 'scene', 'performer', 'nsfw']) {
      expect(text, `first run mentions "${word}"`).not.toContain(word);
    }
  });

  it('proves the key against TMDB before any configuration is written', async () => {
    mountWizard();
    await createAccount();
    typeInto('#tmdb-key', ' abc123 ');

    button('Test').click();
    await settle();

    // Trimmed, and sent in the body: the key is proved before it is stored, so
    // a wrong one never reaches the database. The provider is named because
    // there are several now, and the wizard's field is TMDB's.
    expect(called('/settings/metadata/test')).toEqual([
      expect.objectContaining({
        method: 'POST',
        body: { api_key: 'abc123', provider: 'tmdb' },
      }),
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

  // The acceptance criterion: a wrong key is caught at first run, before any
  // configuration is written.
  it('refuses to finish with a key TMDB rejects, and writes no configuration', async () => {
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

  it('lets metadata stay blank and saves no key', async () => {
    mountWizard();
    await createAccount();
    typeInto('#storage-root', '/data');
    scanOff();

    expect(host.textContent).not.toContain('Skip for now');
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
    system.status = { ...STATUS, needs_setup: true, password_set: true };
    mountWizard();

    expect(host.querySelector('a[href="/settings/indexers"]')?.textContent?.trim()).toBe('Indexers');
    expect(host.querySelector('a[href="/settings/downloads"]')?.textContent?.trim()).toBe('Downloads');
    expect(host.querySelector('a[href="/settings/quality-profiles"]')?.textContent?.trim()).toBe(
      'Download profiles',
    );
  });

  it('does not expose configuration while the administrator account is missing', async () => {
    system.status = { ...STATUS, storage_root: '', needs_setup: true, password_set: false };
    mountWizard();

    expect(host.querySelector('#admin-username')).not.toBeNull();
    expect(host.querySelector('#storage-root')).toBeNull();
    expect(host.querySelector('#tmdb-key')).toBeNull();
    expect(host.querySelector('#thetvdb-key')).toBeNull();

    button('Create account').click();
    await settle();

    expect(host.textContent).toContain('Enter a username for the administrator account');
    expect(called('/setup/admin')).toHaveLength(0);
    expect(called('/storage-root/repoint')).toHaveLength(0);
  });

  it('still refuses an empty storage root', async () => {
    mountWizard();
    await createAccount();

    button('Start Caravan').click();
    await settle();

    expect(host.textContent).toContain('Enter the folder');
    expect(called('/storage-root/repoint')).toHaveLength(0);
    expect(called('/settings/metadata/test')).toHaveLength(0);
    expect(called('/library/rescan')).toHaveLength(0);
    expect(calls.some((call) => call.method === 'PUT')).toBe(false);
  });

  it('proves an unsaved TheTVDB key and PIN before writing them', async () => {
    mountWizard();
    await createAccount();
    typeInto('#thetvdb-key', ' tvdb-key ');
    openPin();
    typeInto('#thetvdb-pin', ' 1234 ');

    testButton('thetvdb').click();
    await settle();

    expect(called('/settings/metadata/test')).toEqual([
      expect.objectContaining({
        method: 'POST',
        body: { api_key: 'tvdb-key', provider: 'thetvdb', pin: '1234' },
      }),
    ]);
    expect(host.textContent).toContain('TheTVDB accepted the credential');
  });

  it('saves a proven TheTVDB pair without requiring TMDB when that step is skipped', async () => {
    mountWizard();
    await createAccount();
    typeInto('#storage-root', '/data');
    typeInto('#thetvdb-key', 'tvdb-key');
    openPin();
    typeInto('#thetvdb-pin', '1234');
    scanOff();

    testButton('thetvdb').click();
    await settle();
    button('Start Caravan').click();
    await settle();

    expect(calls.find((c) => c.method === 'PUT' && c.url.endsWith('/settings'))?.body).toEqual({
      thetvdb_api_key: 'tvdb-key',
      thetvdb_pin: '1234',
    });
    expect(called('/storage-root/repoint')).toHaveLength(1);
  });

  it('refuses to finish with a TheTVDB key the provider rejects, and writes no configuration', async () => {
    responders.push({
      match: '/settings/metadata/test',
      reply: () =>
        jsonResponse(
          { error: 'metadata test failed: unauthorized', code: 'metadata_credential_invalid' },
          502,
        ),
    });
    mountWizard();
    await createAccount();
    typeInto('#storage-root', '/data');
    typeInto('#thetvdb-key', 'wrong');
    scanOff();

    button('Start Caravan').click();
    await settle();

    expect(host.textContent).toContain('unauthorized');
    expect(called('/storage-root/repoint')).toHaveLength(0);
    expect(calls.some((c) => c.method === 'PUT')).toBe(false);
    expect(host.querySelector('#thetvdb-key')).not.toBeNull();
  });

  it('leaves TheTVDB unset when both fields stay blank', async () => {
    mountWizard();
    await createAccount();
    typeInto('#storage-root', '/data');
    scanOff();
    button('Start Caravan').click();
    await settle();

    expect(calls.some((c) => c.method === 'PUT')).toBe(false);
    expect(called('/settings/metadata/test')).toHaveLength(0);
  });
});
