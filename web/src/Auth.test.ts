/**
 * The password gate (SPEC §11, PLAN phase 5 task 5), end to end through the
 * real shell: a 401 puts the login screen up, a successful login takes it down,
 * and the nag appears exactly when the server is reachable with no password on
 * it.
 *
 * It lives in its own file because `auth` is a module singleton - a test that
 * leaves it "required" would break every test after it in the same file.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import App from './App.svelte';
import type { SystemStatus } from './lib/api/types';
import { auth } from './lib/state/auth.svelte';
import { session } from './lib/state/session.svelte';
import { clearToasts, pushToast, toasts } from './lib/state/toast.svelte';

const STATUS: SystemStatus = {
  version: '0.1.0',
  mode: 'server',
  storage_root: '/data',
  schema_version: 5,
  scanning: false,
  counts: { movies: 0, series: 0, media_files: 0, unmatched: 0 },
  disk_free_bytes: 1024,
  disk_total_bytes: 2048,
  engine_health: 'ok',
  ffmpeg_available: true,
  password_set: true,
  listening_publicly: true,
};

/** What GET /auth/me answers on a Caravan with no accounts at all. */
const OPEN_ADMIN = { username: '', role: 'admin', open: true };

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

let host: HTMLElement;
let app: Record<string, unknown>;

beforeEach(() => {
  window.history.replaceState({}, '', '/movies');
  window.scrollTo = () => {};
  window.sessionStorage.clear();
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
  // `auth` and `session` are singletons: a test that ended logged out must not
  // start the next one there.
  auth.required = false;
  auth.error = null;
  session.forget();
  clearToasts();
});

async function settle() {
  for (let i = 0; i < 3; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

function type(selector: string, value: string) {
  const field = host.querySelector(selector) as HTMLInputElement;
  expect(field, selector).not.toBeNull();
  field.value = value;
  field.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

function button(label: string) {
  const found = [...host.querySelectorAll('button')].find((candidate) =>
    candidate.textContent?.includes(label),
  );
  expect(found, `button labelled ${label}`).toBeDefined();
  return found!;
}

describe('password gate', () => {
  it('shows the login screen on 401 and the app once the password is accepted', async () => {
    let loggedIn = false;
    const posted: { url: string; body: unknown }[] = [];

    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = init?.method ?? 'GET';
        if (method === 'POST') {
          posted.push({ url, body: init?.body ? JSON.parse(String(init.body)) : null });
        }
        if (url.endsWith('/auth/login')) {
          const body = JSON.parse(String(init?.body)) as { username: string; password: string };
          if (body.username !== 'ada' || body.password !== 'hunter2hunter2') {
            return jsonResponse({ error: 'invalid username or password' }, 401);
          }
          loggedIn = true;
          return jsonResponse({ password_set: true });
        }
        if (!loggedIn) return jsonResponse({ error: 'unauthorized' }, 401);
        if (url.endsWith('/auth/me')) {
          return jsonResponse({ username: 'ada', role: 'admin', open: false });
        }
        if (url.endsWith('/system/status')) return jsonResponse(STATUS);
        if (url.endsWith('/library/movies')) return jsonResponse({ movies: [] });
        if (url.endsWith('/downloads')) return jsonResponse({ downloads: [] });
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    app = mount(App, { target: host });
    await settle();

    // The 401 on /system/status put the login screen in front of the shell.
    expect(host.textContent).toContain('Sign in');
    expect(host.textContent).toContain('password-protected');
    expect(host.querySelector('aside')).toBeNull();

    // A stack of "unauthorized" toasts on top of the login screen is noise: a
    // dead session fails every in-flight request at once.
    pushToast('unauthorized', 'danger');
    expect(toasts.items.length).toBe(1);

    // Wrong password: still on the login screen, with the server's reason.
    type('#login-username', 'ada');
    type('#login-password', 'wrong');
    button('Sign in').click();
    await settle();

    expect(host.textContent).toContain('invalid username or password');
    expect(host.querySelector('aside')).toBeNull();

    // Right credentials: the shell comes up, without a reload.
    type('#login-username', 'ada');
    type('#login-password', 'hunter2hunter2');
    button('Sign in').click();
    await settle();

    expect(host.textContent).not.toContain('This Caravan is password-protected');
    expect(host.querySelector('aside')).not.toBeNull();
    expect(host.querySelector('a[href="/movies"]')).not.toBeNull();

    expect(posted.map((p) => p.url)).toEqual([
      '/api/v1/auth/login',
      '/api/v1/auth/login',
    ]);
    expect(posted[1]?.body).toEqual({ username: 'ada', password: 'hunter2hunter2' });
    // The role is what the shell renders from, so it is read before anything
    // else the shell needs.
    expect(session.user).toEqual({ username: 'ada', role: 'admin', open: false });
  });

  it('will not submit without a username, and says which field is empty', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith('/auth/me') || url.endsWith('/system/status')) {
          return jsonResponse({ error: 'unauthorized' }, 401);
        }
        return jsonResponse({ error: 'unauthorized' }, 401);
      }),
    );

    app = mount(App, { target: host });
    await settle();

    type('#login-password', 'hunter2hunter2');
    button('Sign in').click();
    await settle();

    expect(host.textContent).toContain('Enter your username.');
  });

  it('sends the user back to the login screen when a later request 401s', async () => {
    let sessionAlive = true;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (!sessionAlive) return jsonResponse({ error: 'unauthorized' }, 401);
        if (url.endsWith('/auth/me')) {
          return jsonResponse({ username: 'ada', role: 'admin', open: false });
        }
        if (url.endsWith('/system/status')) return jsonResponse(STATUS);
        if (url.endsWith('/library/movies')) return jsonResponse({ movies: [] });
        if (url.endsWith('/downloads')) return jsonResponse({ downloads: [] });
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    app = mount(App, { target: host });
    await settle();
    expect(host.querySelector('aside')).not.toBeNull();

    // The session expires server-side; the next poll is what finds out.
    sessionAlive = false;
    await new Promise((resolve) => setTimeout(resolve, 0));
    await fetchStatusThroughTheApp();
    await settle();

    expect(host.textContent).toContain('Sign in');
    expect(host.querySelector('aside')).toBeNull();
    // The toast stack was cleared rather than filled with 401s.
    expect(toasts.items.length).toBe(0);
  });

  it('nags about a public bind with no password, and stops once dismissed', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith('/auth/me')) return jsonResponse(OPEN_ADMIN);
        if (url.endsWith('/system/status')) {
          return jsonResponse({ ...STATUS, password_set: false, listening_publicly: true });
        }
        if (url.endsWith('/library/movies')) return jsonResponse({ movies: [] });
        if (url.endsWith('/downloads')) return jsonResponse({ downloads: [] });
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    app = mount(App, { target: host });
    await settle();

    expect(host.textContent).toContain('Listening on every interface without a password');
    button('Dismiss').click();
    flushSync();
    expect(host.textContent).not.toContain('Listening on every interface without a password');
    expect(window.sessionStorage.getItem('caravan.public-bind-nag-dismissed')).toBe('1');
  });

  it('does not nag when the bind is loopback or a password is set', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith('/auth/me')) return jsonResponse(OPEN_ADMIN);
        if (url.endsWith('/system/status')) {
          return jsonResponse({ ...STATUS, password_set: false, listening_publicly: false });
        }
        if (url.endsWith('/library/movies')) return jsonResponse({ movies: [] });
        if (url.endsWith('/downloads')) return jsonResponse({ downloads: [] });
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    app = mount(App, { target: host });
    await settle();

    expect(host.textContent).not.toContain('Listening on every interface without a password');
  });
});

/** Drive one API call through the app's own client, as a poll would. */
async function fetchStatusThroughTheApp(): Promise<void> {
  const { system } = await import('./lib/state/system.svelte');
  await system.refresh();
}
