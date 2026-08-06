import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import RemotePathMappings from './RemotePathMappings.svelte';
import type { RemotePathMapping } from '../api/types';
import { clearToasts, toasts } from '../state/toast.svelte';

const MAPPING: RemotePathMapping = {
  id: 1,
  remote_path: '/downloads',
  local_path: '/mnt/downloads',
  match_count: 2,
  last_matched_at: '2026-08-05T12:00:00Z',
  created_at: '2026-08-05T12:00:00Z',
  updated_at: '2026-08-05T12:00:00Z',
};

type Call = { url: string; method: string; body: Record<string, string> | null };

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

let host: HTMLElement;
let app: Record<string, unknown>;
let calls: Call[];
let mappings: RemotePathMapping[];

beforeEach(() => {
  calls = [];
  mappings = [MAPPING];
  clearToasts();
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      const body = typeof init?.body === 'string' ? JSON.parse(init.body) : null;
      calls.push({ url, method, body });

      if (method === 'GET' && url.endsWith('/remote-path-mappings')) {
        return jsonResponse({ remote_path_mappings: mappings });
      }
      if (method === 'POST' && url.endsWith('/remote-path-mappings')) {
        const added: RemotePathMapping = {
          id: 2,
          remote_path: body?.remote_path ?? '',
          local_path: body?.local_path ?? '',
          match_count: 0,
          last_matched_at: '',
          created_at: '2026-08-05T12:01:00Z',
          updated_at: '2026-08-05T12:01:00Z',
        };
        mappings = [...mappings, added];
        return jsonResponse(added);
      }

      const item = /\/remote-path-mappings\/(\d+)$/.exec(url);
      if (item && method === 'PUT') {
        const id = Number(item[1]);
        mappings = mappings.map((mapping) =>
          mapping.id === id
            ? {
                ...mapping,
                remote_path: body?.remote_path ?? '',
                local_path: body?.local_path ?? '',
              }
            : mapping,
        );
        return jsonResponse(mappings.find((mapping) => mapping.id === id));
      }
      if (item && method === 'DELETE') {
        const id = Number(item[1]);
        mappings = mappings.filter((mapping) => mapping.id !== id);
        return new Response(null, { status: 204 });
      }
      throw new Error(`unexpected fetch: ${method} ${url}`);
    }),
  );
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
  clearToasts();
});

async function settle() {
  for (let i = 0; i < 3; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

async function render() {
  app = mount(RemotePathMappings, { target: host });
  await settle();
}

function button(label: string, root: ParentNode = host): HTMLButtonElement {
  const found = [...root.querySelectorAll('button')].find((candidate) => {
    const text = candidate.textContent?.trim() ?? '';
    return text === label || text.endsWith(label);
  });
  expect(found, `button labelled ${label}`).toBeDefined();
  return found as HTMLButtonElement;
}

function dialog(): HTMLElement {
  const found = host.querySelector<HTMLElement>('[role="dialog"]');
  expect(found, 'dialog').not.toBeNull();
  return found as HTMLElement;
}

function input(id: string): HTMLInputElement {
  const found = host.querySelector<HTMLInputElement>(`#${id}`);
  expect(found, `input ${id}`).not.toBeNull();
  return found as HTMLInputElement;
}

function type(id: string, value: string) {
  const field = input(id);
  field.value = value;
  field.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

function writeCalls() {
  return calls.filter((call) => call.method !== 'GET');
}

describe('RemotePathMappings', () => {
  it('explains host-local longest-prefix matching and lists saved mappings with diagnostics', async () => {
    await render();

    expect(host.textContent).toContain('longest matching remote prefix');
    expect(host.textContent).toContain('host running Caravan, not on the download client');
    expect(host.textContent).toContain('/downloads');
    expect(host.textContent).toContain('/mnt/downloads');
    expect(host.textContent).toContain('2 matched imports or events.');
    expect(host.textContent).toContain('Last match: 2026-08-05T12:00:00Z.');
  });

  it('validates both paths, canonicalizes trailing separators, and creates a mapping only after they are present', async () => {
    await render();
    button('Add mapping').click();
    await settle();

    expect(button('No changes', dialog()).disabled).toBe(true);
    type('remote-path', '/complete');
    expect(dialog().textContent).toContain('Local path is required.');
    expect(button('Fix errors', dialog()).disabled).toBe(true);
    type('remote-path', '');
    type('local-path', '/srv/media/complete');
    expect(dialog().textContent).toContain('Remote path is required.');
    expect(button('Fix errors', dialog()).disabled).toBe(true);
    expect(writeCalls()).toEqual([]);

    type('remote-path', '/complete///');
    type('local-path', '/srv/media/complete/');
    button('Save', dialog()).click();
    await settle();

    expect(writeCalls()).toEqual([
      {
        method: 'POST',
        url: '/api/v1/remote-path-mappings',
        body: { remote_path: '/complete', local_path: '/srv/media/complete' },
      },
    ]);
    expect(host.textContent).toContain('/srv/media/complete');
    expect(host.textContent).toContain('No imports or events have matched this mapping yet.');
    expect(toasts.items.at(-1)?.message).toBe('Remote path mapping added.');
  });

  it('keeps mapping modal footer actions in a wrapping action row', async () => {
    await render();
    button('Add mapping').click();
    await settle();

    expect(button('Cancel', dialog()).closest('.flex-wrap')).not.toBeNull();
    expect(button('No changes', dialog()).closest('.flex-wrap')).not.toBeNull();
  });

  it('edits and removes a mapping through explicit item actions', async () => {
    await render();
    button('Edit').click();
    await settle();

    type('local-path', '/media/downloads');
    button('Save', dialog()).click();
    await settle();

    expect(writeCalls()[0]).toEqual({
      method: 'PUT',
      url: '/api/v1/remote-path-mappings/1',
      body: { remote_path: '/downloads', local_path: '/media/downloads' },
    });
    expect(host.textContent).toContain('/media/downloads');

    button('Remove').click();
    await settle();
    expect(dialog().textContent).toContain('Caravan will stop translating this remote path.');
    button('Remove', dialog()).click();
    await settle();

    expect(writeCalls()[1]).toEqual({
      method: 'DELETE',
      url: '/api/v1/remote-path-mappings/1',
      body: null,
    });
    expect(host.textContent).toContain('No remote path mappings');
    expect(toasts.items.at(-1)?.message).toBe('Remote path mapping removed.');
  });

  it('uses Modal dirty-close confirmation before discarding an edited mapping', async () => {
    await render();
    button('Add mapping').click();
    await settle();
    type('remote-path', '/downloads');

    const close = dialog().querySelector<HTMLButtonElement>('[aria-label="Close"]');
    expect(close).not.toBeNull();
    close!.click();
    await settle();

    expect(dialog().textContent).toContain('Discard changes?');
    button('Keep editing', dialog()).click();
    await settle();
    expect(dialog().textContent).toContain('Add remote path mapping');
    expect(input('remote-path').value).toBe('/downloads');

    dialog().querySelector<HTMLButtonElement>('[aria-label="Close"]')!.click();
    await settle();
    button('Discard changes', dialog()).click();
    await settle();

    expect(host.querySelector('[role="dialog"]')).toBeNull();
    expect(writeCalls()).toEqual([]);
  });
});
