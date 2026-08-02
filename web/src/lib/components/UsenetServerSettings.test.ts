/**
 * The news-server form, mounted for real against a stubbed /api/v1.
 *
 * What is worth proving here is the consequence of the password never coming
 * back (SPEC §12): the edit form starts blank over a stored password, an
 * untouched field must not clear it, and Test has to be able to say which
 * stored row a blank field belongs to.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import UsenetServerSettings from './UsenetServerSettings.svelte';
import type { UsenetServer } from '../api/types';

const EWEKA: UsenetServer = {
  id: 7,
  name: 'Eweka',
  host: 'news.eweka.nl',
  port: 563,
  tls: true,
  username: 'user',
  has_password: true,
  max_connections: 20,
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
let listed: UsenetServer[];

beforeEach(() => {
  calls = [];
  answers = [];
  listed = [EWEKA];
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
      if (method === 'GET' && url.endsWith('/usenet-servers')) {
        return jsonResponse({ usenet_servers: listed });
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

/** The add/edit form, which carries its own Test button next to the rows'. */
function form(): HTMLFormElement {
  const el = host.querySelector('form');
  expect(el, 'the add/edit form').not.toBeNull();
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
  app = mount(UsenetServerSettings, { target: host });
  await settle();
  clickButton('Edit');
  await settle();
}

describe('UsenetServerSettings', () => {
  it('lists stored servers with where and how they are dialled', async () => {
    app = mount(UsenetServerSettings, { target: host });
    await settle();

    expect(host.textContent).toContain('Eweka');
    expect(host.textContent).toContain('news.eweka.nl:563');
    expect(host.textContent).toContain('TLS');
    expect(host.textContent).toContain('20 conn');
    expect(host.textContent).toContain('Priority 25');
  });

  // The copy rule: these are the built-in engine's own article sources. A user
  // must never read this screen as "you also need an external client".
  it('presents servers as the built-in engine reading articles itself', async () => {
    app = mount(UsenetServerSettings, { target: host });
    await settle();

    expect(host.textContent).toContain("built-in engine");
    expect(host.textContent).not.toMatch(/SABnzbd|NZBGet|download client/i);
  });

  it('offers to add one when there are none, without implying a client is needed', async () => {
    listed = [];
    app = mount(UsenetServerSettings, { target: host });
    await settle();

    expect(host.textContent).toContain('No news servers yet');
    expect(host.textContent).toMatch(/built-in engine/i);
    expect(host.textContent).not.toMatch(/download client/i);
  });

  it('never pre-fills a stored password, and says it is unchanged', async () => {
    await mountAndEdit();

    const password = input('usenet-password');
    expect(password.value).toBe('');
    expect(password.placeholder).toBe('Unchanged');
    // The other half of the login is not a credential and is pre-filled.
    expect(input('usenet-username').value).toBe('user');
    expect(input('usenet-host').value).toBe('news.eweka.nl');
    expect(input('usenet-port').value).toBe('563');
  });

  it('omits the password on save when it was left blank, so the stored one survives', async () => {
    answers = [() => jsonResponse({ ...EWEKA, name: 'Eweka renamed' })];
    await mountAndEdit();

    type('usenet-name', 'Eweka renamed');
    clickButton('Save', form());
    await settle();

    const save = writeCalls().find((c) => c.method === 'PUT');
    expect(save, 'a PUT to the server').toBeDefined();
    expect(save!.url).toContain('/usenet-servers/7');
    expect(save!.body).toMatchObject({
      name: 'Eweka renamed',
      host: 'news.eweka.nl',
      port: 563,
      tls: true,
      username: 'user',
      max_connections: 20,
    });
    // The point: no password key at all, which is what the server reads as
    // "keep what is stored".
    expect(save!.body).not.toHaveProperty('password');
  });

  it('sends a typed password, which replaces the stored one', async () => {
    answers = [() => jsonResponse(EWEKA)];
    await mountAndEdit();

    type('usenet-password', 'rotated');
    clickButton('Save', form());
    await settle();

    const save = writeCalls().find((c) => c.method === 'PUT');
    expect(save!.body).toMatchObject({ password: 'rotated' });
  });

  // Clearing the username is how a server becomes anonymous. A stored password
  // cannot outlive it — the pair is refused outright.
  it('clears the stored password when the username is cleared', async () => {
    answers = [() => jsonResponse({ ...EWEKA, username: '', has_password: false })];
    await mountAndEdit();

    type('usenet-username', '');
    clickButton('Save', form());
    await settle();

    const save = writeCalls().find((c) => c.method === 'PUT');
    expect(save!.body).toMatchObject({ username: '', password: '' });
  });

  it('names the row being edited when testing, so a blank password falls back', async () => {
    answers = [() => jsonResponse({ status: 'ok' })];
    await mountAndEdit();

    clickButton('Test', form());
    await settle();

    const test = writeCalls().find((c) => c.url.endsWith('/usenet-servers/test'));
    expect(test, 'a POST to the unsaved-config probe').toBeDefined();
    expect(test!.body).toMatchObject({ id: 7, host: 'news.eweka.nl', port: 563, tls: true });
    expect(test!.body).not.toHaveProperty('password');
    expect(host.textContent).toContain('Connected');
  });

  it('reports why a test failed rather than only that it did', async () => {
    answers = [() => jsonResponse({ error: '481 authentication failed' }, 502)];
    app = mount(UsenetServerSettings, { target: host });
    await settle();

    clickButton('Test');
    await settle();

    expect(host.textContent).toContain('481 authentication failed');
  });

  it('adds a server with the TLS defaults already filled in', async () => {
    listed = [];
    answers = [() => jsonResponse({ ...EWEKA, id: 9, name: 'New' })];
    app = mount(UsenetServerSettings, { target: host });
    await settle();

    clickButton('Add server');
    await settle();
    // The defaults are on screen, not implied: the user sees the port that
    // will actually be dialled.
    expect(input('usenet-port').value).toBe('563');
    expect(input('usenet-connections').value).toBe('8');
    expect(input('usenet-priority').value).toBe('25');

    type('usenet-name', 'New');
    type('usenet-host', 'news.example.com');
    type('usenet-username', 'u');
    type('usenet-password', 'p');
    clickButton('Save', form());
    await settle();

    const save = writeCalls().find((c) => c.method === 'POST' && !c.url.endsWith('/test'));
    expect(save!.body).toMatchObject({
      name: 'New',
      host: 'news.example.com',
      port: 563,
      tls: true,
      username: 'u',
      password: 'p',
      max_connections: 8,
      priority: 25,
      enabled: true,
    });
  });

  it('moves an untouched port to the other default when TLS is switched off', async () => {
    listed = [];
    app = mount(UsenetServerSettings, { target: host });
    await settle();
    clickButton('Add server');
    await settle();

    expect(input('usenet-port').value).toBe('563');
    clickButton('Use TLS', form());
    await settle();
    expect(input('usenet-port').value).toBe('119');
  });

  it('keeps a port the user chose when TLS is switched off', async () => {
    listed = [];
    app = mount(UsenetServerSettings, { target: host });
    await settle();
    clickButton('Add server');
    await settle();

    type('usenet-port', '9119');
    clickButton('Use TLS', form());
    await settle();
    expect(input('usenet-port').value).toBe('9119');
  });

  it('refuses to submit a configuration it can see is incomplete', async () => {
    listed = [];
    app = mount(UsenetServerSettings, { target: host });
    await settle();
    clickButton('Add server');
    await settle();

    type('usenet-name', 'New');
    clickButton('Save', form());
    await settle();

    expect(host.textContent).toMatch(/hostname/i);
    // Nothing was sent: the round trip is saved, not just the error shown.
    expect(writeCalls()).toHaveLength(0);
  });

  it('removes a server only after the confirmation is accepted', async () => {
    answers = [() => new Response(null, { status: 204 })];
    app = mount(UsenetServerSettings, { target: host });
    await settle();

    clickButton('Remove Eweka');
    await settle();
    const modal = document.querySelector('[role="dialog"]') ?? document.body;
    clickButton('Remove', modal);
    await settle();

    const remove = writeCalls().find((c) => c.method === 'DELETE');
    expect(remove, 'a DELETE to the server').toBeDefined();
    expect(remove!.url).toContain('/usenet-servers/7');
    expect(host.textContent).not.toContain('news.eweka.nl');
  });
});
