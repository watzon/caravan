/**
 * Settings → Users (SPEC §11): the list, and the three writes.
 *
 * The last-admin refusal is the one worth proving twice — the button is off
 * before it can be clicked, and the server's own sentence is shown if the state
 * changed under us — because a Caravan with members and no admin can never be
 * administered again.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Settings from '../routes/Settings.svelte';
import type { User } from '../api/types';
import { session } from '../state/session.svelte';
import { system } from '../state/system.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';

function jsonResponse(body: unknown, status = 200): Response {
  if (status === 204) return new Response(null, { status });
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function user(id: number, username: string, role: 'admin' | 'member'): User {
  return {
    id,
    username,
    role,
    created_at: '2026-08-01T00:00:00Z',
    updated_at: '2026-08-01T00:00:00Z',
  };
}

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let users: User[];
let calls: { url: string; method: string; body: unknown }[];
/** Overrides the next write's response, for the refusals worth rendering. */
let nextFailure: { status: number; error: string } | null;

beforeEach(() => {
  users = [user(1, 'root', 'admin'), user(2, 'ada', 'member')];
  calls = [];
  nextFailure = null;
  session.user = { username: 'root', role: 'admin', open: false, adult: false };
  system.status = null;
  host = document.createElement('div');
  document.body.appendChild(host);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      const body = init?.body ? JSON.parse(String(init.body)) : null;
      if (method !== 'GET') calls.push({ url, method, body });

      if (url.endsWith('/auth/me')) return jsonResponse(session.user);
      if (method === 'GET' && url.endsWith('/users')) return jsonResponse({ users });
      if (nextFailure && method !== 'GET') {
        const failure = nextFailure;
        nextFailure = null;
        return jsonResponse({ error: failure.error }, failure.status);
      }
      if (method === 'POST' && url.endsWith('/users')) {
        return jsonResponse(user(3, body.username, body.role), 201);
      }
      if (method === 'DELETE' || method === 'POST') return jsonResponse(null, 204);
      if (url.endsWith('/settings')) return jsonResponse({});
      if (url.endsWith('/system/status')) return jsonResponse({});
      throw new Error(`unexpected fetch: ${method} ${url}`);
    }),
  );
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  vi.unstubAllGlobals();
  session.forget();
  system.status = null;
  clearToasts();
});

async function settle() {
  for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

async function openUsers() {
  app = mount(Settings, { target: host, props: { section: 'users' } }) as Record<string, unknown>;
  await settle();
}

function button(label: string, root: ParentNode = host): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.textContent?.trim() === label,
  );
  expect(found, `button labelled ${label}`).toBeDefined();
  return found!;
}

/**
 * The dialog's own button. "Add user" is on the card as well as in the dialog
 * it opens, so an unscoped lookup would keep finding the one that is never
 * disabled.
 */
function dialogButton(label: string): HTMLButtonElement {
  const dialog = document.querySelector('[role="dialog"]');
  expect(dialog, 'an open dialog').not.toBeNull();
  return button(label, dialog!);
}

/** Each account row as text, which is what the list is for. */
function rowText(): string[] {
  return [...host.querySelectorAll('li')].map((li) => li.textContent ?? '');
}

function type(selector: string, value: string) {
  const field = document.querySelector(selector) as HTMLInputElement;
  expect(field, selector).not.toBeNull();
  field.value = value;
  field.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

describe('Users settings', () => {
  it('lists every account with its role, and marks the one that is you', async () => {
    await openUsers();

    const rows = rowText();
    expect(rows).toHaveLength(2);
    expect(rows[0]).toContain('root');
    expect(rows[0]).toContain('ADMIN');
    expect(rows[0]).toContain('You');
    expect(rows[1]).toContain('ada');
    expect(rows[1]).toContain('MEMBER');
    // A hash has nowhere to appear, but neither does the word.
    expect(host.textContent?.toLowerCase()).not.toContain('argon2id$');
  });

  it('creates an account with a username, a password and a role', async () => {
    await openUsers();

    button('Add user').click();
    await settle();

    // A short password cannot be submitted at all.
    type('#user-username', 'sam');
    type('#user-password', 'short');
    expect(dialogButton('Add user').hasAttribute('disabled')).toBe(true);

    type('#user-password', 'a good password');
    const role = document.querySelector('#user-role') as HTMLSelectElement;
    role.value = 'admin';
    role.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();

    dialogButton('Add user').click();
    await settle();

    expect(calls).toContainEqual({
      url: '/api/v1/users',
      method: 'POST',
      body: { username: 'sam', password: 'a good password', role: 'admin' },
    });
    // The new row is on screen without a refetch, and the dialog is gone.
    expect(rowText().some((row) => row.includes('sam'))).toBe(true);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(toasts.items.map((t) => t.message)).toEqual(['Added sam.']);
  });

  /**
   * The first account is what closes an open Caravan, and the server refuses to
   * let it be a member — a gated server with no admin can never be administered
   * again. The form must not offer the refusal as its default.
   */
  it('defaults the first account on an open server to Admin', async () => {
    users = [];
    await openUsers();

    button('Add user').click();
    await settle();

    const role = document.querySelector('#user-role') as HTMLSelectElement;
    expect(role).not.toBeNull();
    expect(role.value).toBe('admin');

    type('#user-username', 'chris');
    type('#user-password', 'a good password');
    dialogButton('Add user').click();
    await settle();

    expect(calls).toContainEqual({
      url: '/api/v1/users',
      method: 'POST',
      body: { username: 'chris', password: 'a good password', role: 'admin' },
    });
  });

  /** Once an admin exists, a second account is a housemate until told otherwise. */
  it('defaults a later account to Member', async () => {
    await openUsers();

    button('Add user').click();
    await settle();

    expect((document.querySelector('#user-role') as HTMLSelectElement).value).toBe('member');
  });

  /**
   * A username with a space on either end is refused rather than trimmed:
   * " ada" and "ada" would otherwise be one account under a name only one of
   * them can type.
   */
  it('refuses a username padded with spaces before it is sent', async () => {
    await openUsers();
    button('Add user').click();
    await settle();

    type('#user-username', ' ada ');
    type('#user-password', 'a good password');

    expect(dialogButton('Add user').hasAttribute('disabled')).toBe(true);
    expect(calls.filter((c) => c.method === 'POST')).toEqual([]);
  });

  it('resets somebody else password without asking for the old one', async () => {
    await openUsers();

    button('Reset password').click();
    await settle();

    expect(document.querySelector('#user-username')).toBeNull();
    type('#user-password', 'a new password');
    dialogButton('Set password').click();
    await settle();

    expect(calls).toContainEqual({
      url: '/api/v1/users/1/password',
      method: 'POST',
      body: { new_password: 'a new password' },
    });
  });

  it('deletes an account behind a confirm, and drops the row', async () => {
    await openUsers();

    // Two rows, so the trash button of the member is the second one.
    const trash = [...host.querySelectorAll<HTMLButtonElement>('li button')].filter((b) =>
      b.textContent?.includes('Delete'),
    );
    expect(trash).toHaveLength(2);
    // The lone admin's is off: the server refuses it, so the click would be a
    // dead end.
    expect(trash[0]!.hasAttribute('disabled')).toBe(true);
    expect(trash[1]!.hasAttribute('disabled')).toBe(false);

    trash[1]!.click();
    await settle();
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('ada can no longer');

    dialogButton('Delete').click();
    await settle();

    expect(calls).toContainEqual({ url: '/api/v1/users/2', method: 'DELETE', body: null });
    expect(rowText()).toHaveLength(1);
    expect(rowText()[0]).toContain('root');
  });

  /**
   * The disabled button is a courtesy; the server is what actually refuses, and
   * its sentence explains the consequence better than a generic one would. It
   * belongs beside the button that caused it, not in a toast that outlives the
   * dialog.
   */
  it('shows the last-admin refusal in the dialog rather than losing it', async () => {
    users = [user(1, 'root', 'admin'), user(2, 'second', 'admin')];
    await openUsers();

    const trash = [...host.querySelectorAll<HTMLButtonElement>('li button')].filter((b) =>
      b.textContent?.includes('Delete'),
    );
    nextFailure = { status: 409, error: 'this is the last admin; make someone else an admin first' };
    trash[0]!.click();
    await settle();
    dialogButton('Delete').click();
    await settle();

    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog).not.toBeNull();
    expect(dialog?.textContent).toContain('this is the last admin');
    // The row is still there.
    expect(rowText()).toHaveLength(2);
  });
});
