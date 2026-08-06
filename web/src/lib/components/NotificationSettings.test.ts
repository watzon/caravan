import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import type { NotificationWebhook } from '../api/types';
import { clearToasts, toasts } from '../state/toast.svelte';
import NotificationSettings from './NotificationSettings.svelte';

const HOOK: NotificationWebhook = {
  id: 1,
  name: 'Automation',
  has_url: true,
  on_grab: true,
  on_import: false,
  on_health: true,
  enabled: true,
  last_event_id: 12,
  created_at: '2026-08-05T12:00:00Z',
  updated_at: '2026-08-05T12:00:00Z',
};

let host: HTMLElement;
let app: Record<string, unknown>;
let hooks: NotificationWebhook[];
let writes: { url: string; method: string; body: unknown }[];

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

async function settle() {
  for (let i = 0; i < 3; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

function button(label: string, root: ParentNode = host): HTMLButtonElement {
  const found = [...root.querySelectorAll('button')].find((item) => item.textContent?.trim() === label);
  expect(found, `button ${label}`).toBeDefined();
  return found as HTMLButtonElement;
}

function type(id: string, value: string) {
  const input = host.querySelector(`#${id}`) as HTMLInputElement;
  expect(input).not.toBeNull();
  input.value = value;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  hooks = [HOOK];
  writes = [];
  clearToasts();
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    const body = typeof init?.body === 'string' ? JSON.parse(init.body) : null;
    if (method !== 'GET') writes.push({ url, method, body });
    if (method === 'GET' && url.endsWith('/notification-webhooks')) return json({ notification_webhooks: hooks });
    if (method === 'POST' && url.endsWith('/notification-webhooks')) {
      const created = { ...HOOK, ...(body as object), id: 2, last_event_id: 12 };
      hooks = [...hooks, created];
      return json(created, 201);
    }
    if (method === 'PUT' && url.endsWith('/notification-webhooks/1')) {
      const updated = { ...HOOK, ...(body as object) };
      hooks = [updated];
      return json(updated);
    }
    if (method === 'POST' && url.endsWith('/notification-webhooks/1/test')) return json({ ok: true });
    throw new Error(`unexpected ${method} ${url}`);
  }));
  app = mount(NotificationSettings, { target: host });
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
  clearToasts();
});

describe('Notification settings', () => {
  it('lists exact subscriptions and sends a test payload', async () => {
    await settle();
    expect(host.textContent).toContain('Automation');
    expect(host.textContent).toContain('Grabbed');
    expect(host.textContent).toContain('Health');
    expect(host.textContent).not.toContain('Imported');
    expect(host.textContent).toContain('Endpoint configured');
    expect(host.textContent).not.toContain('hooks.example');

    button('Test').click();
    await settle();
    expect(writes[0]).toEqual({ url: '/api/v1/notification-webhooks/1/test', method: 'POST', body: null });
    expect(toasts.items.at(-1)?.message).toContain('Test delivered');
  });

  it('validates the URL and creates a webhook with explicit event switches', async () => {
    await settle();
    button('Add webhook').click();
    flushSync();
    type('notification-name', '  Media room  ');
    type('notification-url', 'file:///tmp/hook');
    expect(host.textContent).toContain('Enter an absolute HTTP or HTTPS URL.');
    expect(button('Fix errors').disabled).toBe(true);

    type('notification-url', 'https://events.example/hook');
    button('Save').click();
    await settle();
    expect(writes[0]).toEqual({
      url: '/api/v1/notification-webhooks',
      method: 'POST',
      body: {
        name: 'Media room',
        url: 'https://events.example/hook',
        on_grab: true,
        on_import: true,
        on_health: true,
        enabled: true,
      },
    });
    expect(host.textContent).toContain('Media room');
  });

  it('keeps add-modal footer actions in a wrapping action row', async () => {
    await settle();
    button('Add webhook').click();
    flushSync();

    const dialog = host.querySelector<HTMLElement>('[role="dialog"]');
    expect(dialog).not.toBeNull();
    expect(button('Cancel', dialog!).closest('.flex-wrap')).not.toBeNull();
    expect(button('Fix errors', dialog!).closest('.flex-wrap')).not.toBeNull();
  });

  it('keeps the write-only URL when an edit omits it and announces an update', async () => {
    await settle();
    button('Edit').click();
    flushSync();
    expect((host.querySelector('#notification-url') as HTMLInputElement).value).toBe('');

    type('notification-name', 'Renamed automation');
    button('Save').click();
    await settle();

    expect(writes[0]).toEqual({
      url: '/api/v1/notification-webhooks/1',
      method: 'PUT',
      body: {
        name: 'Renamed automation',
        on_grab: true,
        on_import: false,
        on_health: true,
        enabled: true,
      },
    });
    expect(toasts.items.at(-1)?.message).toBe('Notification webhook updated.');
  });
});
