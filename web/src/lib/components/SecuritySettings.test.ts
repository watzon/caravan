/**
 * The Security settings section (SPEC §11, PLAN phase 5 task 5): setting,
 * changing and clearing the password, and showing/regenerating the API key.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Settings from '../routes/Settings.svelte';
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
  passwordSet = false;
  posted = [];
  system.status = null;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (method === 'POST') {
        posted.push({ url, body: init?.body ? JSON.parse(String(init.body)) : null });
      }
      if (url.endsWith('/settings/password')) {
        const body = JSON.parse(String(init?.body)) as { new_password: string };
        passwordSet = body.new_password !== '';
        return jsonResponse({ password_set: passwordSet });
      }
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
  app = mount(Settings, { target: host });
  await settle();
  button('Security').click();
  flushSync();
}

describe('Security settings', () => {
  it('sets a password, then changes it with the current one', async () => {
    await openSecurityTab();

    // No password yet: nothing to prove, and the nag says why it matters.
    expect(host.querySelector('#security-current-password')).toBeNull();
    expect(host.textContent).toContain('Listening on every interface without a password');

    // A short password cannot be submitted at all.
    type('#security-new-password', 'short');
    expect(button('Set password').hasAttribute('disabled')).toBe(true);

    type('#security-new-password', 'a good password');
    button('Set password').click();
    await settle();

    expect(posted[0]?.url).toBe('/api/v1/settings/password');
    expect(posted[0]?.body).toEqual({ current_password: '', new_password: 'a good password' });
    // The status refresh flipped the section into "password set" mode.
    expect(host.querySelector('#security-current-password')).not.toBeNull();
    expect(host.textContent).not.toContain('Listening on every interface without a password');
    // The typed password is not left sitting in the form.
    expect((host.querySelector('#security-new-password') as HTMLInputElement).value).toBe('');

    type('#security-current-password', 'a good password');
    type('#security-new-password', 'a better password');
    button('Change password').click();
    await settle();

    expect(posted[1]?.body).toEqual({
      current_password: 'a good password',
      new_password: 'a better password',
    });
  });

  it('clears the password with an empty new password', async () => {
    passwordSet = true;
    await openSecurityTab();

    type('#security-current-password', 'a good password');
    button('Clear password').click();
    await settle();

    expect(posted[0]?.body).toEqual({ current_password: 'a good password', new_password: '' });
    expect(host.querySelector('#security-current-password')).toBeNull();
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
