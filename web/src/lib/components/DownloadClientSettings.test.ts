/**
 * The download-client form, mounted for real against a stubbed /api/v1.
 *
 * What is worth proving here is the consequence of the credential never coming
 * back (SPEC §12): the edit form starts blank over a stored password, an
 * untouched field must not clear it, and Test has to be able to say which
 * stored row a blank field belongs to.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import DownloadClientSettings from './DownloadClientSettings.svelte';
import type { DownloadClient, DownloadClientTypeInfo } from '../api/types';

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
    supported: false,
  },
];

const QBIT: DownloadClient = {
  id: 7,
  type: 'qbittorrent',
  name: 'qBit',
  url: 'http://127.0.0.1:8080',
  username: 'admin',
  has_password: true,
  has_api_key: false,
  max_concurrent: 0,
  category: 'caravan',
  priority: 25,
  enabled: true,
};

const UNSUPPORTED: DownloadClient = {
  ...QBIT,
  type: 'sabnzbd',
  name: 'Legacy SABnzbd',
  username: '',
  has_password: false,
  has_api_key: false,
};

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
/** Answers for the write/test endpoints, consumed in order. */
let answers: Array<() => Response>;
let availableTypes: DownloadClientTypeInfo[];
let listedClients: DownloadClient[];

beforeEach(() => {
  calls = [];
  listedClients = [QBIT];
  availableTypes = TYPES;
  answers = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      calls.push({
        url,
        method,
        body: typeof init?.body === 'string' ? JSON.parse(init.body) : null,
      });
      if (method === 'GET' && url.endsWith('/download-clients/types')) {
        return jsonResponse({ types: availableTypes });
      }
      if (method === 'GET' && url.endsWith('/download-clients')) {
        return jsonResponse({ download_clients: listedClients });
      }
      // The routing pickers render inside this screen and own their own
      // settings fetch. It is answered here rather than from the queue so it
      // cannot consume an answer meant for a save, a test or a delete.
      if (method === 'GET' && url.endsWith('/settings')) {
        return jsonResponse({});
      }
      const answer = answers.shift();
      if (!answer) throw new Error(`unexpected fetch: ${method} ${url}`);
      return answer();
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

function clickButton(label: string, root: ParentNode = host) {
  const button = [...root.querySelectorAll('button')].find(
    (b) => (b.textContent ?? '').trim() === label,
  );
  expect(button, `a button labelled "${label}"`).toBeDefined();
  button!.click();
  flushSync();
}

/**
 * The add/edit dialog. Its fields live in a form and its Test, Save and Cancel
 * in the modal footer, so the dialog — not the form — is what scopes a click
 * away from the rows' own Test buttons.
 */
function editor(): HTMLElement {
  const el = host.querySelector<HTMLElement>('[role="dialog"]');
  expect(el, 'the add/edit dialog').not.toBeNull();
  expect(el!.querySelector('form'), 'the add/edit form inside it').not.toBeNull();
  return el!;
}

function input(id: string): HTMLInputElement {
  const el = host.querySelector<HTMLInputElement>(`#${id}`);
  expect(el, `an input #${id}`).not.toBeNull();
  return el!;
}

function type(id: string, value: string) {
  const el = input(id);
  el.value = value;
  el.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

function writeCalls() {
  return calls.filter((c) => c.method !== 'GET');
}

async function mountAndEdit() {
  app = mount(DownloadClientSettings, { target: host });
  await settle();
  clickButton('Edit');
  await settle();
}

describe('DownloadClientSettings', () => {
  it('exposes full client strings where row text can truncate', async () => {
    app = mount(DownloadClientSettings, { target: host });
    await settle();

    const clientName = [...host.querySelectorAll<HTMLElement>('.truncate')].find(
      (element) => element.textContent?.trim() === QBIT.name,
    );
    const clientURL = [...host.querySelectorAll<HTMLElement>('.truncate')].find(
      (element) => element.textContent?.trim() === QBIT.url,
    );
    expect(clientName?.getAttribute('title')).toBe(QBIT.name);
    expect(clientURL?.getAttribute('title')).toBe(QBIT.url);
    const enabledStatus = [...host.querySelectorAll('span')].find(
      (element) =>
        element.textContent?.trim() === 'Enabled' && !element.classList.contains('sr-only'),
    );
    expect(enabledStatus).toBeDefined();
    expect(host.querySelector('.size-2')?.getAttribute('aria-hidden')).toBe('true');
  });

  it('disables unavailable types when adding a client and explains why', async () => {
    app = mount(DownloadClientSettings, { target: host });
    await settle();
    expect(host.textContent).toContain('qBit');
    expect(host.textContent).toContain('http://127.0.0.1:8080');
    expect(host.textContent).not.toContain('Not supported yet');

    clickButton('Add client');
    await settle();

    const sabnzbd = [...editor().querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === 'SABnzbd',
    );
    expect(sabnzbd).toBeDefined();
    expect(sabnzbd!.disabled).toBe(true);
    expect(sabnzbd!.getAttribute('title') ?? '').toContain('cannot connect');
    expect(host.textContent).toContain('Unsupported types are unavailable for new clients');
    expect(host.querySelector('#client-username')).not.toBeNull();
    expect(host.querySelector('#client-api-key')).toBeNull();
    expect(host.querySelector('#client-priority')?.closest('[data-settings-advanced]')).not.toBeNull();
    expect(host.querySelector('#client-max-concurrent')?.closest('[data-settings-advanced]')).not.toBeNull();
    expect(host.querySelector('#client-url')?.closest('[data-settings-advanced]')).toBeNull();
  });

  it('cannot save a new client when its type is unsupported', async () => {
    availableTypes = TYPES.map((type) => ({ ...type, supported: false }));
    app = mount(DownloadClientSettings, { target: host });
    await settle();

    clickButton('Add client');
    await settle();
    type('client-name', 'Unavailable qBit');
    type('client-url', 'http://nas.local:8080');
    type('client-username', 'admin');

    const save = [...editor().querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === 'Fix errors',
    );
    expect(save!.disabled).toBe(true);
    expect(host.textContent).toContain('not available for new clients');
    expect(writeCalls()).toHaveLength(0);
  });

  it('never pre-fills a stored credential, and says it is unchanged', async () => {
    await mountAndEdit();

    const password = input('client-password');
    expect(password.value).toBe('');
    expect(password.placeholder).toBe('Unchanged');
    // The other half of the login is not a credential and is pre-filled.
    expect(input('client-username').value).toBe('admin');
  });

  it('shows No changes for an untouched edit', async () => {
    await mountAndEdit();

    const save = [...editor().querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === 'No changes',
    );
    expect(save).toBeDefined();
    expect(save!.disabled).toBe(true);
    expect(writeCalls()).toHaveLength(0);
  });

  it('keeps a dirty client draft open until Modal discards it', async () => {
    await mountAndEdit();
    type('client-name', 'Unsaved client');

    const dialog = editor();
    dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle();
    expect(host.textContent).toContain('Discard changes');
    clickButton('Keep editing');

    const backdrop = host.querySelector<HTMLElement>('[data-modal-backdrop]');
    expect(backdrop).not.toBeNull();
    backdrop!.click();
    await settle();
    expect(host.textContent).toContain('Discard changes');
    clickButton('Keep editing');

    const close = dialog.querySelector<HTMLButtonElement>('button[aria-label="Close"]');
    expect(close).not.toBeNull();
    close!.click();
    await settle();
    expect(host.textContent).toContain('Discard changes');
    clickButton('Discard changes');
    await settle();
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });

  it('refreshes the edit snapshot after saving, so unchanged Save is immediately disabled', async () => {
    const saved = { ...QBIT, name: 'qBit renamed' };
    answers = [() => jsonResponse(saved)];
    await mountAndEdit();

    type('client-name', saved.name);
    clickButton('Save', editor());
    await settle();

    expect(editor().querySelector('form')).not.toBeNull();
    const save = [...editor().querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === 'No changes',
    );
    expect(save).toBeDefined();
    expect(save!.disabled).toBe(true);
    expect(writeCalls().find((call) => call.method === 'PUT')?.body).toMatchObject({
      name: saved.name,
    });
  });

  it('omits the password on save when it was left blank, so the stored one survives', async () => {
    answers = [() => jsonResponse({ ...QBIT, name: 'qBit renamed' })];
    await mountAndEdit();

    type('client-name', 'qBit renamed');
    expect(
      [...editor().querySelectorAll('button')].find(
        (button) => button.textContent?.trim() === 'Save',
      )!.disabled,
    ).toBe(false);
    clickButton('Save', editor());
    await settle();

    const save = writeCalls().find((c) => c.method === 'PUT');
    expect(save, 'a PUT to the client').toBeDefined();
    expect(save!.url).toContain('/download-clients/7');
    expect(save!.body).toMatchObject({ name: 'qBit renamed', username: 'admin' });
    // The point: no credential key at all, which is what the server reads as
    // "keep what is stored".
    expect(save!.body).not.toHaveProperty('password');
    expect(save!.body).not.toHaveProperty('api_key');
  });

  it('sends a password the user actually typed', async () => {
    answers = [() => jsonResponse(QBIT)];
    await mountAndEdit();

    type('client-password', 'rotated');
    clickButton('Save', editor());
    await settle();

    const save = writeCalls().find((c) => c.method === 'PUT');
    expect(save!.body).toMatchObject({ password: 'rotated' });
  });

  it('tests unsaved draft values without saving them', async () => {
    answers = [() => jsonResponse({ status: 'ok' })];
    await mountAndEdit();

    type('client-name', 'qBit draft');
    type('client-url', 'http://nas.local:8080');
    clickButton('Test', editor());
    await settle();

    const test = writeCalls().find((call) => call.url.endsWith('/download-clients/test'));
    expect(test, 'a POST to the unsaved-config test').toBeDefined();
    // The id is what lets the server fall back to the stored password.
    expect(test!.body).toMatchObject({
      id: 7,
      name: 'qBit draft',
      url: 'http://nas.local:8080',
    });
    expect(test!.body).not.toHaveProperty('password');
    expect(writeCalls().some((call) => call.method === 'PUT')).toBe(false);
    expect(host.textContent).toContain('Reachable');
    expect(host.querySelector('[title="Reachable"]')).not.toBeNull();
  });

  it('reports a failed test with the client’s own complaint', async () => {
    answers = [() => jsonResponse({ error: 'download client test failed: 403 Forbidden' }, 502)];
    app = mount(DownloadClientSettings, { target: host });
    await settle();

    clickButton('Test');
    await settle();

    expect(calls.some((c) => c.url.endsWith('/download-clients/7/test'))).toBe(true);
    expect(host.textContent).toContain('403 Forbidden');
  });

  it('keeps existing unsupported clients editable for safe changes', async () => {
    listedClients = [UNSUPPORTED];
    answers = [() => jsonResponse({ ...UNSUPPORTED, name: 'Legacy SABnzbd renamed' })];
    app = mount(DownloadClientSettings, { target: host });
    await settle();

    expect(host.textContent).toContain('Not supported yet');
    clickButton('Edit');
    await settle();
    type('client-name', 'Legacy SABnzbd renamed');

    const save = [...editor().querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === 'Save',
    );
    expect(save!.disabled).toBe(false);
    save!.click();
    await settle();
    expect(writeCalls().find((call) => call.method === 'PUT')?.body).toMatchObject({
      type: 'sabnzbd',
      name: 'Legacy SABnzbd renamed',
    });
  });

  it('refuses to save a configuration the server would reject anyway', async () => {
    app = mount(DownloadClientSettings, { target: host });
    await settle();
    clickButton('Add client');
    await settle();

    type('client-name', 'new');
    type('client-url', 'nas.local');
    type('client-username', 'admin');
    const save = [...editor().querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === 'Fix errors',
    );
    expect(save!.disabled).toBe(true);

    expect(writeCalls()).toHaveLength(0);
    expect(host.textContent).toContain('http://');
  });

  it('removes a client after the confirmation', async () => {
    answers = [() => new Response(null, { status: 204 })];
    app = mount(DownloadClientSettings, { target: host });
    await settle();

    const remove = [...host.querySelectorAll('button')].find((b) =>
      (b.textContent ?? '').includes('Remove qBit'),
    );
    expect(remove).toBeDefined();
    remove!.click();
    flushSync();

    // Nothing is deleted until the modal is confirmed.
    expect(writeCalls()).toHaveLength(0);
    clickButton('Remove');
    await settle();

    const del = writeCalls().find((c) => c.method === 'DELETE');
    expect(del!.url).toContain('/download-clients/7');
    expect(host.textContent).toContain('No download clients yet');
  });
});

describe('DownloadClientSettings concurrency', () => {
  // The per-client cap is what stops one seedbox taking every slot, so it has
  // to round-trip through the form like any other field.
  it('sends the per-client cap', async () => {
    answers = [() => jsonResponse(QBIT)];
    await mountAndEdit();

    type('client-max-concurrent', '2');
    clickButton('Save', editor());
    await settle();

    const save = writeCalls().find((c) => c.method === 'PUT');
    expect(save, 'a PUT to the client').toBeDefined();
    expect(save!.body).toMatchObject({ max_concurrent: 2 });
  });

  it('blocks invalid priority and concurrent-download values before any POST or PUT', async () => {
    await mountAndEdit();

    type('client-priority', '1.5');
    expect(host.textContent).toContain('Priority must be a whole number of zero or greater.');
    editor().querySelector('form')!.dispatchEvent(
      new Event('submit', { bubbles: true, cancelable: true }),
    );
    await settle();
    expect(writeCalls()).toEqual([]);

    type('client-priority', '1');
    type('client-max-concurrent', '-1');
    expect(host.textContent).toContain(
      'Max concurrent downloads must be blank or a whole number of zero or greater.',
    );
    editor().querySelector('form')!.dispatchEvent(
      new Event('submit', { bubbles: true, cancelable: true }),
    );
    await settle();
    expect(writeCalls()).toEqual([]);
  });
  // Blank carries the explicit no-cap value through the SPA. It is not parsed
  // as zero before the request leaves the browser.
  it('sends a cleared cap as null', async () => {
    answers = [() => jsonResponse(QBIT)];
    await mountAndEdit();

    type('client-max-concurrent', '');
    clickButton('Save', editor());
    await settle();

    expect(writeCalls().find((c) => c.method === 'PUT')!.body).toMatchObject({ max_concurrent: null });
  });

  it('round-trips an unset client cap as an empty editor field', async () => {
    listedClients = [{ ...QBIT, max_concurrent: null }];
    await mountAndEdit();

    expect(input('client-max-concurrent').value).toBe('');
  });
});
