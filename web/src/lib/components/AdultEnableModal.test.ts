/**
 * The adult enable setup (PLAN Part 2 phase 8, was phase 10 task 5).
 *
 * Two acceptance criteria, both negative. With no stash-box endpoint the module
 * CANNOT end up on, and cancelling changes nothing. The server enforces both —
 * validation runs before any write and `adult_enabled` is committed last — so
 * the modal's job is to never claim otherwise.
 *
 * The third thing these pin is the two-step: every instance route 404s while
 * the module is off, so the modal cannot look up whether an endpoint exists. It
 * posts a bare enable and lets the SERVER say, revealing the form only on
 * `adult_credential_absent`.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import AdultEnableModal from './AdultEnableModal.svelte';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const ABSENT = () =>
  jsonResponse(
    {
      error: 'a stash-box endpoint is required to enable adult content',
      code: 'adult_credential_absent',
    },
    400,
  );

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: { url: string; method: string; body: Record<string, unknown> | null }[];
let enabled: number;
let closed: number;
/** What POST /settings/adult answers, per call, oldest first. */
let replies: (() => Response)[];

beforeEach(() => {
  calls = [];
  enabled = 0;
  closed = 0;
  replies = [];
  host = document.createElement('div');
  document.body.appendChild(host);

  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({
        url: String(input),
        method: init?.method ?? 'GET',
        body: typeof init?.body === 'string' ? JSON.parse(init.body) : null,
      });
      return (replies.shift() ?? (() => jsonResponse({ enabled: true })))();
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

function mountModal() {
  app = mount(AdultEnableModal, {
    target: host,
    props: {
      onclose: () => (closed += 1),
      onenabled: () => (enabled += 1),
    },
  });
  flushSync();
}

function button(label: string): HTMLButtonElement {
  const found = [...host.querySelectorAll('button')].find((b) => b.textContent?.trim() === label);
  expect(found, `button labelled ${label}`).toBeDefined();
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

describe('AdultEnableModal', () => {
  // The exposure defaults come BEFORE the commit — the whole reason the modal
  // opens on them rather than on a form.
  it('opens on the exposure defaults and asks for nothing', () => {
    mountModal();

    expect(host.textContent).toContain('DLNA stays off');
    expect(host.textContent).toContain('Prepared drives leave it out');
    expect(host.textContent).toContain('Members see nothing until granted');
    // No form: the modal does not yet know whether one is needed.
    expect(host.querySelector('#adult-enable-api-key')).toBeNull();
    expect(calls).toEqual([]);
  });

  // The re-enable path. The instances survived the switch-off, so the enable
  // carries nothing and the server makes zero upstream calls.
  it('enables bare when the server already has an endpoint', async () => {
    mountModal();

    button('Enable adult content').click();
    await settle();

    expect(calls).toEqual([
      expect.objectContaining({ method: 'POST', body: { enabled: true } }),
    ]);
    expect(enabled).toBe(1);
    expect(host.querySelector('#adult-enable-api-key')).toBeNull();
  });

  // The first-enable path, and the reason the form is a fallback: the modal
  // cannot introspect the instance table through a 404 wall, so the server is
  // what reveals the form.
  it('reveals the first-instance form when the server says none is configured', async () => {
    replies = [ABSENT];
    mountModal();

    button('Enable adult content').click();
    await settle();

    expect(enabled).toBe(0);
    expect(host.textContent).toContain('a stash-box endpoint is required');
    expect(host.querySelector('#adult-enable-preset')).not.toBeNull();
    expect(host.querySelector('#adult-enable-api-key')).not.toBeNull();
  });

  it('enables with the preset instance it showed', async () => {
    replies = [ABSENT];
    mountModal();
    button('Enable adult content').click();
    await settle();

    typeInto('#adult-enable-api-key', ' sb-key ');
    button('Enable adult content').click();
    await settle();

    expect(calls[1]?.body).toEqual({
      enabled: true,
      instance: { name: 'StashDB', endpoint: 'https://stashdb.org/graphql', api_key: 'sb-key' },
    });
    expect(enabled).toBe(1);
  });

  it('sends a custom endpoint verbatim', async () => {
    replies = [ABSENT];
    mountModal();
    button('Enable adult content').click();
    await settle();

    pick('#adult-enable-preset', '');
    typeInto('#adult-enable-name', 'House box');
    typeInto('#adult-enable-endpoint-url', 'https://stash.example/graphql');
    typeInto('#adult-enable-api-key', 'k');
    button('Enable adult content').click();
    await settle();

    expect(calls[1]?.body).toEqual({
      enabled: true,
      instance: { name: 'House box', endpoint: 'https://stash.example/graphql', api_key: 'k' },
    });
  });

  // The acceptance criterion: a refused credential cannot leave the module on.
  // The server never wrote anything, and the modal says so on the form the
  // mistake is on.
  it('stays off and keeps the form when the box refuses the key', async () => {
    replies = [
      ABSENT,
      () =>
        jsonResponse(
          { error: 'stash-box test failed: unauthorized', code: 'adult_credential_invalid' },
          502,
        ),
    ];
    mountModal();
    button('Enable adult content').click();
    await settle();

    typeInto('#adult-enable-api-key', 'bad-key');
    button('Enable adult content').click();
    await settle();

    expect(enabled).toBe(0);
    expect(host.textContent).toContain('stash-box test failed: unauthorized');
    // Still on the form, with the key that was wrong still editable.
    expect(host.querySelector('#adult-enable-api-key')).not.toBeNull();
  });

  // A missing name is refused before a request is made: the server would refuse
  // it too, but a round trip to learn a field is empty is one nobody needs.
  it('will not post an instance with no name', async () => {
    replies = [ABSENT];
    mountModal();
    button('Enable adult content').click();
    await settle();

    typeInto('#adult-enable-name', '   ');
    button('Enable adult content').click();
    await settle();

    expect(calls).toHaveLength(1);
    expect(host.textContent).toContain('Give this stash-box a name.');
  });

  it('announces a non-credential enable failure without revealing a form', async () => {
    replies = [() => jsonResponse({ error: 'settings service unavailable' }, 500)];
    mountModal();

    button('Enable adult content').click();
    await settle();

    const alert = host.querySelector('[role="alert"]');
    expect(alert?.textContent).toContain('settings service unavailable');
    expect(host.textContent).toContain('DLNA stays off');
    expect(host.querySelector('#adult-enable-api-key')).toBeNull();
    expect(enabled).toBe(0);
  });

  // Cancel leaves everything off — which is easy to guarantee, because nothing
  // was ever written.
  it('writes nothing when cancelled from either step', async () => {
    mountModal();
    button('Cancel').click();
    flushSync();
    expect(closed).toBe(1);
    expect(calls).toEqual([]);

    replies = [ABSENT];
    button('Enable adult content').click();
    await settle();
    button('Cancel').click();
    flushSync();

    expect(closed).toBe(2);
    expect(enabled).toBe(0);
    // The one call is the bare enable the server refused; it wrote nothing.
    expect(calls).toHaveLength(1);
  });
});
