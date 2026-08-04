/**
 * The Security settings section (SPEC §11): changing your OWN password, and
 * showing/regenerating the API key. Creating accounts and resetting somebody
 * else's password are the Users section's, not this one's.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Settings from '../routes/Settings.svelte';
import { session } from '../state/session.svelte';
import { system } from '../state/system.svelte';
import type { SystemStatus } from '../api/types';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const STATUS: SystemStatus = {
  version: 'test',
  mode: 'server',
  storage_root: '/data',
  schema_version: 5,
  scanning: false,
  counts: { movies: 0, series: 0, media_files: 0, unmatched: 0 },
  disk_free_bytes: 0,
  disk_total_bytes: 0,
  engine_health: 'ok',
  ffmpeg_available: true,
  password_set: false,
  listening_publicly: true,
};

let host: HTMLElement;
let app: Record<string, unknown>;
let passwordSet = false;
let posted: { url: string; body: unknown }[] = [];

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  passwordSet = true;
  posted = [];
  system.status = null;
  session.user = { username: 'ada', role: 'admin', open: false, adult: false };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (method === 'POST') {
        posted.push({ url, body: init?.body ? JSON.parse(String(init.body)) : null });
      }
      if (url.endsWith('/settings/password')) return jsonResponse({ password_set: true });
      if (url.endsWith('/settings/apikey')) return jsonResponse({ api_key: 'newkey' });
      if (url.endsWith('/settings')) return jsonResponse({ api_key: 'oldkey' });
      if (url.endsWith('/system/status')) return jsonResponse({ ...STATUS, password_set: passwordSet });
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
  system.status = null;
  session.forget();
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

function type(selector: string, value: string) {
  const field = host.querySelector(selector) as HTMLInputElement;
  expect(field, selector).not.toBeNull();
  field.value = value;
  field.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

async function openSecurityTab() {
  // The shell fetches status before any screen mounts; this section reads
  // password_set from it.
  await system.refresh();
  app = mount(Settings, { target: host, props: { section: 'security' } });
  await settle();
}

describe('Security settings', () => {
  it('changes the signed-in account own password, proving the current one', async () => {
    await openSecurityTab();

    // Neither field filled in, and a too-short new password, are both refused
    // before a request is made.
    expect(button('Change password').hasAttribute('disabled')).toBe(true);
    type('#security-current-password', 'a good password');
    type('#security-new-password', 'short');
    expect(button('Change password').hasAttribute('disabled')).toBe(true);

    type('#security-new-password', 'a better password');
    button('Change password').click();
    await settle();

    expect(posted[0]?.url).toBe('/api/v1/settings/password');
    expect(posted[0]?.body).toEqual({
      current_password: 'a good password',
      new_password: 'a better password',
    });
    // Neither password is left sitting in the form.
    expect((host.querySelector('#security-current-password') as HTMLInputElement).value).toBe('');
    expect((host.querySelector('#security-new-password') as HTMLInputElement).value).toBe('');
  });

  /**
   * There is no password of "mine" on a server with no accounts, and clearing
   * one is no longer a thing at all: reopening a Caravan means deleting every
   * account, which is the Users section's business.
   */
  it('offers Users instead of a password form when nothing is signed in', async () => {
    session.user = { username: '', role: 'admin', open: true, adult: false };
    passwordSet = false;
    await openSecurityTab();

    expect(host.querySelector('#security-current-password')).toBeNull();
    expect(host.querySelector('#security-new-password')).toBeNull();
    expect(host.textContent).not.toContain('Clear password');
    expect(host.querySelector('a[href="/settings/users"]')).not.toBeNull();
    expect(host.textContent).toContain('Listening on every interface without a password');
  });

  it('shows the API key and swaps in the regenerated one', async () => {
    await openSecurityTab();

    expect((host.querySelector('#security-api-key') as HTMLInputElement).value).toBe('oldkey');
    button('Regenerate key').click();
    await settle();

    expect(posted[0]?.url).toBe('/api/v1/settings/apikey');
    expect((host.querySelector('#security-api-key') as HTMLInputElement).value).toBe('newkey');
  });
});
