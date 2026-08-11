/**
 * Settings → Metadata → Stash-box (PLAN Part 2 phase 8).
 *
 * Three things this card must never get wrong, so they are what these test:
 *
 *  - the stored API key is write-only, so nothing on screen may render it —
 *    `has_api_key` drives a badge and that is all;
 *  - the endpoint is immutable after creation, so the edit form has no field
 *    for it (an input that cannot be used is an offer the screen cannot keep);
 *  - a delete the server refuses names the libraries and items that still
 *    depend on the instance, and that sentence is shown as it arrived.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import StashboxSettings from './StashboxSettings.svelte';
import type { StashboxInstance } from '../api/types';
import { clearToasts } from '../state/toast.svelte';

function jsonResponse(body: unknown, status = 200): Response {
  if (status === 204) return new Response(null, { status });
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const TPDB: StashboxInstance = {
  id: 1,
  provider_id: 'stashbox',
  name: 'ThePornDB',
  endpoint: 'https://theporndb.net/graphql',
  has_api_key: true,
  library_count: 1,
  item_count: 12,
};

const KEYLESS: StashboxInstance = {
  id: 2,
  provider_id: 'stashbox:stashdb',
  name: 'StashDB',
  endpoint: 'https://stashdb.org/graphql',
  has_api_key: false,
  library_count: 0,
  item_count: 0,
};

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let instances: StashboxInstance[];
let calls: { url: string; method: string; body: Record<string, unknown> | null }[];
/** Per-call override for the next write, oldest first. */
let replies: (() => Response)[];

beforeEach(() => {
  instances = [{ ...TPDB }, { ...KEYLESS }];
  calls = [];
  replies = [];
  clearToasts();
  host = document.createElement('div');
  document.body.appendChild(host);

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
      const override = method === 'GET' ? undefined : replies.shift();
      if (override) return override();
      if (method === 'GET') return jsonResponse({ instances });
      if (method === 'DELETE') return jsonResponse(null, 204);
      return jsonResponse({ status: 'ok' });
    }),
  );
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  vi.unstubAllGlobals();
});

async function settle() {
  for (let i = 0; i < 5; i += 1) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

async function mountCard() {
  app = mount(StashboxSettings, { target: host });
  await settle();
}

function button(label: string): HTMLButtonElement {
  const found = [...host.querySelectorAll('button')].find((b) => b.textContent?.trim() === label);
  expect(found, `button labelled ${label}`).toBeDefined();
  return found as HTMLButtonElement;
}

/** A button inside a row, so "Test" on one instance is not "Test" on the other. */
function rowButton(providerID: string, label: string): HTMLButtonElement {
  const row = host.querySelector(`[data-stashbox-row="${providerID}"]`);
  expect(row, `row for ${providerID}`).not.toBeNull();
  const found = [...row!.querySelectorAll('button')].find((b) => b.textContent?.trim() === label);
  expect(found, `${label} in ${providerID}'s row`).toBeDefined();
  return found as HTMLButtonElement;
}

function dialogButton(label: string): HTMLButtonElement {
  const dialog = host.querySelector('[role="dialog"]');
  expect(dialog, 'dialog').not.toBeNull();
  const found = [...dialog!.querySelectorAll('button')].find(
    (b) => b.textContent?.trim() === label,
  );
  expect(found, `dialog button labelled ${label}`).toBeDefined();
  return found as HTMLButtonElement;
}

/** The row's remove control, which is an icon labelled only for readers. */
function removeButton(name: string): HTMLButtonElement {
  const found = host.querySelector(`button[title="Remove ${name}"]`);
  expect(found, `remove control for ${name}`).not.toBeNull();
  return found as HTMLButtonElement;
}

function typeInto(selector: string, value: string): void {
  const field = host.querySelector(selector) as HTMLInputElement | null;
  expect(field, selector).not.toBeNull();
  field!.value = value;
  field!.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

function pick(selector: string, value: string): void {
  const select = host.querySelector(selector) as HTMLSelectElement | null;
  expect(select, selector).not.toBeNull();
  select!.value = value;
  select!.dispatchEvent(new Event('change', { bubbles: true }));
  flushSync();
}

describe('StashboxSettings', () => {
  it('lists each instance with its endpoint, key badge and use counts', async () => {
    await mountCard();

    expect(host.textContent).toContain('ThePornDB');
    expect(host.textContent).toContain('https://theporndb.net/graphql');
    expect(host.textContent).toContain('Key stored');
    expect(host.textContent).toContain('Used by 1 library · 12 items');

    expect(host.textContent).toContain('StashDB');
    expect(host.textContent).toContain('No key');
    expect(host.textContent).toContain('Used by 0 libraries · 0 items');
  });

  // The key is write-only server-side; the badge is the whole of what may be
  // said about it. A field pre-filled with anything key-shaped would be the one
  // way a stored credential could leave the server.
  it('never renders a stored key, in the row or in the edit form', async () => {
    await mountCard();

    rowButton('stashbox', 'Edit').click();
    flushSync();

    const key = host.querySelector('#stashbox-api-key') as HTMLInputElement;
    expect(key.value).toBe('');
    expect(host.textContent).toContain('A key is stored. Leave blank to keep it.');
  });

  // Editing an endpoint would re-point every pinned item at a box that never
  // minted those UUIDs. The server refuses it, and the form does not offer it.
  it('has no endpoint field on the edit form, and says why', async () => {
    await mountCard();

    rowButton('stashbox', 'Edit').click();
    flushSync();

    expect(host.querySelector('#stashbox-endpoint')).toBeNull();
    expect(host.textContent).toContain(
      'The endpoint cannot change because items use IDs from this stash-box. Add another instance for a different box.',
    );
    // The value is still stated, because "which box is this" is the question
    // the name alone cannot answer.
    expect(host.textContent).toContain('https://theporndb.net/graphql');
  });

  it('saves an edit as name plus key, with the endpoint left unchanged', async () => {
    await mountCard();

    rowButton('stashbox', 'Edit').click();
    flushSync();
    typeInto('#stashbox-name', 'TPDB (house)');
    typeInto('#stashbox-api-key', ' new-key ');
    dialogButton('Save').click();
    await settle();

    const put = calls.find((c) => c.method === 'PUT');
    expect(put?.url).toContain('/adult/stashbox-instances/1');
    // A blank endpoint is how the server is told "unchanged".
    expect(put?.body).toEqual({ name: 'TPDB (house)', endpoint: '', api_key: 'new-key' });
  });

  it('posts the preset endpoint the add form was filled from', async () => {
    await mountCard();

    button('Add stash-box').click();
    flushSync();
    pick('#stashbox-preset', 'fansdb');
    typeInto('#stashbox-api-key', 'fk');
    dialogButton('Save').click();
    await settle();

    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({
      name: 'FansDB',
      endpoint: 'https://fansdb.cc/graphql',
      api_key: 'fk',
    });
  });

  // The add form has no id to test against, so it proves the endpoint and key
  // through the body-shaped route — before the instance exists.
  it('tests the typed endpoint before it is saved', async () => {
    await mountCard();

    button('Add stash-box').click();
    flushSync();
    pick('#stashbox-preset', 'stashdb');
    dialogButton('Test').click();
    await settle();

    const test = calls.find((c) => c.url.endsWith('/adult/stashbox-instances/test'));
    expect(test).toMatchObject({
      method: 'POST',
      body: { name: 'StashDB', endpoint: 'https://stashdb.org/graphql', api_key: '' },
    });
    expect(host.textContent).toContain('The stash-box answered.');
  });

  it('tests a stored instance against its own id and reports the refusal', async () => {
    replies = [() => jsonResponse({ error: 'stash-box test failed: unauthorized' }, 502)];
    await mountCard();

    rowButton('stashbox', 'Test').click();
    await settle();

    expect(calls.some((c) => c.url.endsWith('/adult/stashbox-instances/1/test'))).toBe(true);
    expect(host.textContent).toContain('stash-box test failed: unauthorized');
  });

  // The refusal names the libraries and items that still depend on the
  // instance. Paraphrasing it would lose the only thing the user can act on.
  it('surfaces the delete refusal verbatim and keeps the instance', async () => {
    replies = [
      () =>
        jsonResponse(
          {
            error:
              'ThePornDB is used by 1 library and 12 items; move them to another instance first',
          },
          409,
        ),
    ];
    await mountCard();

    removeButton('ThePornDB').click();
    flushSync();
    dialogButton('Remove').click();
    await settle();

    expect(host.querySelector('[role="alert"]')?.textContent).toContain(
      'ThePornDB is used by 1 library and 12 items; move them to another instance first',
    );
    expect(host.textContent).toContain('ThePornDB');
  });

  it('removes an instance nothing depends on', async () => {
    await mountCard();

    removeButton('StashDB').click();
    flushSync();
    instances = [{ ...TPDB }];
    dialogButton('Remove').click();
    await settle();

    expect(calls.some((c) => c.method === 'DELETE' && c.url.endsWith('/adult/stashbox-instances/2'))).toBe(
      true,
    );
    expect(host.querySelector('[data-stashbox-row="stashbox:stashdb"]')).toBeNull();
  });

  it('offers the add form from the empty state', async () => {
    instances = [];
    await mountCard();

    expect(host.textContent).toContain('No stash-box endpoint yet');
    button('Add stash-box').click();
    flushSync();

    expect(host.querySelector('#stashbox-endpoint')).not.toBeNull();
  });
});
