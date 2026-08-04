/**
 * The adult enable setup (PLAN phase 10 task 5).
 *
 * The acceptance criterion is a negative one, so that is what these test: with
 * a missing or refused stash-box credential the module CANNOT end up on, and
 * cancelling changes nothing. The server enforces it too — validation runs
 * before any write and `adult_enabled` is committed last — so the modal's job
 * is to never claim otherwise.
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

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: { url: string; method: string; body: Record<string, unknown> | null }[];
let enabled: { endpoint: string; apiKey: string }[];
let closed: number;
/** What POST /settings/adult answers; the default is a working credential. */
let enableReply: () => Response;

beforeEach(() => {
  calls = [];
  enabled = [];
  closed = 0;
  enableReply = () => jsonResponse({ enabled: true });
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
      return enableReply();
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

function mountModal(props: { initialEndpoint?: string; initialApiKey?: string } = {}) {
  app = mount(AdultEnableModal, {
    target: host,
    props: {
      ...props,
      onclose: () => (closed += 1),
      onenabled: (committed: { endpoint: string; apiKey: string }) => enabled.push(committed),
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

/** Fill in a key and walk to the confirm step. */
function toConfirm(key = 'sb-key') {
  typeInto('#adult-enable-api-key', key);
  button('Continue').click();
  flushSync();
}

describe('AdultEnableModal', () => {
  it('opens on the credential step with the ThePornDB preset selected', () => {
    mountModal();

    const select = host.querySelector('#adult-enable-endpoint') as HTMLSelectElement;
    expect(select.value).toBe('tpdb');
    // The custom URL field only exists once Custom is chosen.
    expect(host.querySelector('#adult-enable-endpoint-url')).toBeNull();
    expect(host.textContent).not.toContain('DLNA stays off');
  });

  // Nothing to authenticate with is refused before a request is made: the
  // server would refuse it too (adult_credential_absent), but a round trip to
  // learn the field is empty is a round trip nobody needs.
  it('will not advance without an API key, and asks for nothing', () => {
    mountModal();

    button('Continue').click();
    flushSync();

    expect(host.textContent).toContain('Enter the stash-box API key');
    expect(calls).toEqual([]);
  });

  // The confirm step is where the exposure defaults are restated, and it comes
  // BEFORE the commit — the whole reason it is a step rather than a footnote.
  it('restates the three exposure defaults before anything is enabled', () => {
    mountModal();
    toConfirm();

    expect(host.textContent).toContain('DLNA stays off');
    expect(host.textContent).toContain('Prepared drives leave it out');
    expect(host.textContent).toContain('Members see nothing until granted');
    expect(calls).toEqual([]);
  });

  it('enables with the credential it showed, blank endpoint for the preset', async () => {
    mountModal();
    toConfirm(' sb-key ');

    button('Enable adult content').click();
    await settle();

    expect(calls).toEqual([
      expect.objectContaining({
        method: 'POST',
        body: { enabled: true, stashbox_endpoint: '', stashbox_api_key: 'sb-key' },
      }),
    ]);
    expect(enabled).toEqual([{ endpoint: '', apiKey: 'sb-key' }]);
  });

  it('sends a custom endpoint verbatim', async () => {
    mountModal({ initialEndpoint: 'https://stash.example/graphql', initialApiKey: 'k' });

    expect((host.querySelector('#adult-enable-endpoint') as HTMLSelectElement).value).toBe(
      'custom',
    );
    button('Continue').click();
    flushSync();
    button('Enable adult content').click();
    await settle();

    expect(calls[0]?.body).toEqual({
      enabled: true,
      stashbox_endpoint: 'https://stash.example/graphql',
      stashbox_api_key: 'k',
    });
  });

  // The acceptance criterion: a refused credential cannot leave the module on.
  // The server never wrote anything, and the modal says so where the mistake
  // is — on the field, back on the credential step.
  it('stays off and returns to the credential step when the provider refuses', async () => {
    enableReply = () =>
      jsonResponse(
        { error: 'stash-box test failed: unauthorized', code: 'adult_credential_invalid' },
        502,
      );
    mountModal();
    toConfirm('bad-key');

    button('Enable adult content').click();
    await settle();

    expect(enabled).toEqual([]);
    expect(host.textContent).toContain('stash-box test failed: unauthorized');
    // Back on step 1, with the key that was wrong still editable.
    expect(host.querySelector('#adult-enable-api-key')).not.toBeNull();
    expect(host.textContent).not.toContain('DLNA stays off');
  });

  it('reports a missing credential the server refused without turning anything on', async () => {
    enableReply = () =>
      jsonResponse(
        {
          error: 'a stash-box API key is required to enable adult content',
          code: 'adult_credential_absent',
        },
        400,
      );
    mountModal();
    toConfirm('whitespace-only-server-side');

    button('Enable adult content').click();
    await settle();

    expect(enabled).toEqual([]);
    expect(host.textContent).toContain('a stash-box API key is required');
  });

  // Cancel leaves everything off — which is easy to guarantee, because nothing
  // was ever written.
  it('writes nothing when cancelled from either step', async () => {
    mountModal();
    button('Cancel').click();
    flushSync();
    expect(closed).toBe(1);
    expect(calls).toEqual([]);

    toConfirm();
    button('Cancel').click();
    flushSync();

    expect(closed).toBe(2);
    expect(enabled).toEqual([]);
    expect(calls).toEqual([]);
  });

  // Editing the credential must not be a trip back through the whole dialog,
  // but it must go through the confirm step again — the enable only ever
  // commits what the user last looked at.
  it('goes back to the credential step without enabling', () => {
    mountModal();
    toConfirm();

    button('Back').click();
    flushSync();

    expect(host.querySelector('#adult-enable-api-key')).not.toBeNull();
    expect(calls).toEqual([]);
  });
});
