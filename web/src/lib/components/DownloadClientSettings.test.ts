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

beforeEach(() => {
  calls = [];
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
        return jsonResponse({ types: TYPES });
      }
      if (method === 'GET' && url.endsWith('/download-clients')) {
        return jsonResponse({ download_clients: [QBIT] });
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
  it('lists stored clients and flags a backend this build cannot talk to', async () => {
    app = mount(DownloadClientSettings, { target: host });
    await settle();

    expect(host.textContent).toContain('qBit');
    expect(host.textContent).toContain('http://127.0.0.1:8080');
    // The row's own backend is supported, so nothing is flagged yet.
    expect(host.textContent).not.toContain('Not supported yet');

    clickButton('Edit');
    await settle();
    // Switching to a backend the server reported as unsupported says so.
    clickButton('SABnzbd', editor());
    await settle();
    expect(host.textContent).toContain('cannot talk to it yet');
  });

  it('never pre-fills a stored credential, and says it is unchanged', async () => {
    await mountAndEdit();

    const password = input('client-password');
    expect(password.value).toBe('');
    expect(password.placeholder).toBe('Unchanged');
    // The other half of the login is not a credential and is pre-filled.
    expect(input('client-username').value).toBe('admin');
  });

  it('omits the password on save when it was left blank, so the stored one survives', async () => {
    answers = [() => jsonResponse({ ...QBIT, name: 'qBit renamed' })];
    await mountAndEdit();

    type('client-name', 'qBit renamed');
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

  it('tests the edit form against the stored row so a blank credential resolves', async () => {
    answers = [() => jsonResponse({ status: 'ok' })];
    await mountAndEdit();

    type('client-url', 'http://nas.local:8080');
    clickButton('Test', editor());
    await settle();

    const test = writeCalls().find((c) => c.url.endsWith('/download-clients/test'));
    expect(test, 'a POST to the unsaved-config test').toBeDefined();
    // The id is what lets the server fall back to the stored password.
    expect(test!.body).toMatchObject({ id: 7, url: 'http://nas.local:8080' });
    expect(test!.body).not.toHaveProperty('password');
    expect(host.textContent).toContain('Reachable');
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

  it('renders the credential fields the chosen backend uses, and no others', async () => {
    app = mount(DownloadClientSettings, { target: host });
    await settle();
    clickButton('Add client');
    await settle();

    expect(host.querySelector('#client-username')).not.toBeNull();
    expect(host.querySelector('#client-api-key')).toBeNull();

    clickButton('SABnzbd', editor());
    await settle();
    expect(host.querySelector('#client-username')).toBeNull();
    expect(host.querySelector('#client-api-key')).not.toBeNull();
  });

  it('refuses to save a configuration the server would reject anyway', async () => {
    app = mount(DownloadClientSettings, { target: host });
    await settle();
    clickButton('Add client');
    await settle();

    type('client-name', 'new');
    type('client-url', 'nas.local');
    type('client-username', 'admin');
    clickButton('Save', editor());
    await settle();

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

  // Blank means "no limit", never "one at a time": a cap the user did not set
  // must not be able to stop downloads.
  it('sends a cleared cap as unlimited', async () => {
    answers = [() => jsonResponse(QBIT)];
    await mountAndEdit();

    type('client-max-concurrent', '');
    clickButton('Save', editor());
    await settle();

    expect(writeCalls().find((c) => c.method === 'PUT')!.body).toMatchObject({ max_concurrent: 0 });
  });
});
