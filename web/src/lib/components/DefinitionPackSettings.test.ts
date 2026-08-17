/**
 * Settings → Indexers → owner definition packs. The card owns preview, legal
 * acceptance, install, activate, and rollback against the exact multipart/JSON
 * contract. Tokens and public keys stay in component memory.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount as mountComponent, unmount } from 'svelte';
import DefinitionPackSettings from './DefinitionPackSettings.svelte';
import type { DefinitionPackPreview, DefinitionPackRevision } from '../api/types';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const INSTALLED: DefinitionPackRevision = {
  source: 'community',
  revision: '2026.08.14',
  install_state: 'installed',
  pending: false,
  active: false,
  last_known_good: false,
  definition_count: 12,
  runnable_count: 10,
  archive_digest: 'sha256:archive-aaaaaaaaaaaaaaaa',
  manifest_digest: 'sha256:manifest-bbbbbbbbbbbbbbbb',
  license_digest: 'sha256:license-cccccccccccccccc',
  notice_digest: 'sha256:notice-dddddddddddddddd',
  signature_fingerprint: 'aa:bb:cc:dd',
  license_expression: 'MIT',
  provenance: 'signed-community',
  minimum_caravan_version: '0.1.0',
  installed_at: '2026-08-14T00:00:00Z',
  accepted_at: '2026-08-14T00:00:00Z',
  accepted_by_user_id: 1,
};

const ZERO_RUNNABLE: DefinitionPackRevision = {
  ...INSTALLED,
  revision: '2026.08.01',
  runnable_count: 0,
};

const PENDING: DefinitionPackRevision = {
  ...INSTALLED,
  revision: '2026.08.10',
  pending: true,
  active: false,
};

const PREVIEW: DefinitionPackPreview = {
  source: 'community',
  revision: '2026.08.20',
  archive_digest: 'sha256:preview-archive',
  manifest_digest: 'sha256:preview-manifest',
  license_digest: 'sha256:preview-license',
  signature_fingerprint: '11:22:33:44',
  license: 'Permission is hereby granted.\nNo warranty.',
  notice: 'Community notice line.',
  token: 'preview-token-secret',
  expires_at: '2026-08-15T12:05:00.000Z',
};

interface Call {
  url: string;
  method: string;
  body: unknown;
  headers: Record<string, string>;
}

let host: HTMLElement;
let app: Record<string, unknown>;
let calls: Call[];
let revisions: DefinitionPackRevision[];
let previewReply: () => Response;
let installReply: () => Response;
let activateReply: () => Response;
let rollbackReply: () => Response;
let listReply: (() => Response) | null;

function stubFetch() {
  calls = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      calls.push({
        url,
        method,
        body: init?.body ?? null,
        headers: (init?.headers ?? {}) as Record<string, string>,
      });
      if (url.endsWith('/definition-packs') && method === 'GET') {
        return listReply ? listReply() : jsonResponse({ revisions });
      }
      if (url.endsWith('/definition-packs/preview')) return previewReply();
      if (url.endsWith('/definition-packs/install')) return installReply();
      if (url.endsWith('/definition-packs/activate')) return activateReply();
      if (url.endsWith('/definition-packs/rollback')) return rollbackReply();
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
}

beforeEach(() => {
  revisions = [{ ...INSTALLED }, { ...ZERO_RUNNABLE }, { ...PENDING }];
  previewReply = () => jsonResponse(PREVIEW);
  installReply = () => jsonResponse({ ...INSTALLED, revision: PREVIEW.revision }, 201);
  activateReply = () => jsonResponse({ restart_required: true }, 202);
  rollbackReply = () => jsonResponse({ ...PENDING, pending: false });
  listReply = null;
  stubFetch();
  host = document.createElement('div');
  document.body.appendChild(host);
  vi.useFakeTimers();
  vi.setSystemTime(new Date('2026-08-15T12:00:00.000Z'));
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

async function settle() {
  await vi.advanceTimersByTimeAsync(0);
  await Promise.resolve();
  flushSync();
}

function click(label: string) {
  const button = [...host.querySelectorAll('button')].find((candidate) =>
    (candidate.textContent ?? '').includes(label),
  );
  expect(button, `button labelled "${label}"`).toBeDefined();
  button!.click();
  flushSync();
}

function mount(component: unknown, options: { target: HTMLElement }): Record<string, unknown> {
  const mounted = mountComponent(component as never, options) as Record<string, unknown>;
  flushSync();
  click('Manage packs');
  expect(host.querySelector('[role="dialog"]')).not.toBeNull();
  return mounted;
}

function field(id: string): HTMLInputElement | HTMLTextAreaElement {
  const el = host.querySelector<HTMLInputElement | HTMLTextAreaElement>(`#${id}`);
  expect(el, `#${id}`).not.toBeNull();
  return el!;
}

function type(id: string, value: string) {
  const el = field(id);
  el.value = value;
  el.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

function chooseArchive(file = new File(['zip-bytes'], 'community.zip', { type: 'application/zip' })) {
  const input = host.querySelector<HTMLInputElement>('input[type="file"]');
  expect(input).not.toBeNull();
  Object.defineProperty(input, 'files', { configurable: true, value: [file] });
  input!.dispatchEvent(new Event('change', { bubbles: true }));
  flushSync();
  return file;
}

async function fillAndPreview(file?: File) {
  const archive = chooseArchive(file);
  type('definition-pack-signer', 'owner-1');
  type('definition-pack-public-key', 'cHVibGljLWtleQ==');
  click('Preview pack');
  await settle();
  return archive;
}

describe('DefinitionPackSettings', () => {
  it('keeps the installer in a modal and clears sensitive state when discarded', async () => {
    app = mountComponent(DefinitionPackSettings, { target: host });
    await settle();

    const launcher = [...host.querySelectorAll('button')].find((button) =>
      button.textContent?.includes('Manage packs'),
    );
    expect(launcher).toBeDefined();
    expect(host.querySelector('[role="dialog"]')).toBeNull();
    expect(host.querySelector('#definition-pack-signer')).toBeNull();
    expect(host.querySelector('#definition-pack-public-key')).toBeNull();

    launcher!.focus();
    launcher!.click();
    flushSync();
    expect(host.querySelector('[role="dialog"]')).not.toBeNull();
    type('definition-pack-signer', 'owner-1');
    type('definition-pack-public-key', 'cHVibGljLWtleQ==');

    const close = host.querySelector<HTMLButtonElement>('[role="dialog"] button[aria-label="Close"]');
    expect(close).not.toBeNull();
    close!.click();
    await settle();
    expect(host.textContent).toContain('Discard changes?');
    click('Discard changes');
    await settle();

    expect(host.querySelector('[role="dialog"]')).toBeNull();
    expect(host.querySelector('#definition-pack-signer')).toBeNull();
    expect(host.textContent).not.toContain('cHVibGljLWtleQ==');
    expect(document.activeElement).toBe(launcher);
  });

  it('lists installed revisions and retries a failed load', async () => {
    listReply = () => new Response(JSON.stringify({ error: 'store down' }), { status: 500 });
    app = mount(DefinitionPackSettings, { target: host });
    await settle();

    expect(host.textContent).toContain('store down');
    listReply = () => jsonResponse({ revisions });
    click('Retry');
    await settle();

    expect(host.textContent).toContain('community');
    expect(host.textContent).toContain('2026.08.14');
    expect(host.textContent).toContain('State: installed');
    expect(host.textContent).toContain('12 definitions, 10 runnable');
    expect(host.textContent).toContain('Pending restart');
    expect(host.textContent).toContain('sha256:archive-aaaaaaaaaaaaaaaa');
    expect(host.textContent).toContain('MIT');
    expect(host.textContent).toContain('signed-community');
    expect(host.textContent).toContain('0.1.0');
  });

  it('previews the exact multipart fields and renders license text, not HTML', async () => {
    previewReply = () =>
      jsonResponse({
        ...PREVIEW,
        license: '<img src=x onerror=alert(1)>Granted.',
        notice: '<script>bad()</script>Notice.',
      });
    app = mount(DefinitionPackSettings, { target: host });
    await settle();
    const archive = await fillAndPreview();

    const preview = calls.find((call) => call.url.endsWith('/preview'));
    expect(preview?.method).toBe('POST');
    expect(preview?.headers.Accept).toBe('application/json');
    expect(preview?.headers['Content-Type']).toBeUndefined();
    expect(preview?.body).toBeInstanceOf(FormData);
    const fields = preview!.body as FormData;
    expect(fields.get('archive')).toBe(archive);
    expect(fields.get('signer_key_id')).toBe('owner-1');
    expect(fields.get('public_key')).toBe('cHVibGljLWtleQ==');

    expect(host.querySelector('img')).toBeNull();
    expect(host.querySelector('script')).toBeNull();
    expect(host.textContent).toContain('<img src=x onerror=alert(1)>Granted.');
    expect(host.textContent).toContain('<script>bad()</script>Notice.');
    expect(host.textContent).toContain('community');
    expect(host.textContent).toContain('2026.08.20');
    expect(host.textContent).toContain('sha256:preview-archive');
    expect(host.textContent).toContain('11:22:33:44');
    expect(host.textContent).not.toContain('preview-token-secret');
    const legal = host.querySelectorAll('[data-pack-legal]');
    expect(legal).toHaveLength(2);
    expect(legal[0]?.getAttribute('tabindex')).toBe('0');
  });

  it('requires license acceptance before Install is enabled', async () => {
    app = mount(DefinitionPackSettings, { target: host });
    await settle();
    await fillAndPreview();

    const install = [...host.querySelectorAll('button')].find((button) =>
      button.textContent?.includes('Install pack'),
    );
    expect(install?.disabled).toBe(true);

    const accept = host.querySelector<HTMLInputElement>('#definition-pack-accept');
    expect(accept).not.toBeNull();
    accept!.checked = true;
    accept!.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    expect(install?.disabled).toBe(false);
  });

  it('resends the same archive on install and clears file, key, and token from memory and the DOM', async () => {
    app = mount(DefinitionPackSettings, { target: host });
    await settle();
    const archive = await fillAndPreview();
    const accept = host.querySelector<HTMLInputElement>('#definition-pack-accept')!;
    accept.checked = true;
    accept.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    click('Install pack');
    await settle();

    const install = calls.find((call) => call.url.endsWith('/install'));
    expect(install?.headers['Content-Type']).toBeUndefined();
    const fields = install?.body as FormData;
    expect(fields.get('archive')).toBe(archive);
    expect(fields.get('signer_key_id')).toBe('owner-1');
    expect(fields.get('public_key')).toBe('cHVibGljLWtleQ==');
    expect(fields.get('source')).toBe('community');
    expect(fields.get('token')).toBe('preview-token-secret');

    expect((field('definition-pack-signer') as HTMLInputElement).value).toBe('');
    expect((field('definition-pack-public-key') as HTMLTextAreaElement).value).toBe('');
    expect(host.querySelector<HTMLInputElement>('input[type="file"]')?.value).toBe('');
    expect(host.textContent).not.toContain('cHVibGljLWtleQ==');
    expect(host.textContent).not.toContain('preview-token-secret');
    expect(host.querySelector('#definition-pack-accept')).toBeNull();
    expect(calls.filter((call) => call.url.endsWith('/definition-packs') && call.method === 'GET')).toHaveLength(2);
  });

  it('disables Install after the preview token expires and asks for a new preview', async () => {
    app = mount(DefinitionPackSettings, { target: host });
    await settle();
    await fillAndPreview();
    const accept = host.querySelector<HTMLInputElement>('#definition-pack-accept')!;
    accept.checked = true;
    accept.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();

    vi.setSystemTime(new Date('2026-08-15T12:05:00.000Z'));
    await vi.advanceTimersByTimeAsync(1000);
    flushSync();

    const install = [...host.querySelectorAll('button')].find((button) =>
      button.textContent?.includes('Install pack'),
    );
    expect(install?.disabled).toBe(true);
    expect(host.textContent).toContain('Preview the pack again');

    previewReply = () => jsonResponse({
      ...PREVIEW,
      expires_at: '2026-08-15T12:10:00.000Z',
    });
    const preview = [...host.querySelectorAll('button')].find((button) =>
      button.textContent?.includes('Preview pack'),
    );
    expect(preview?.disabled).toBe(false);
    preview!.click();
    await settle();

    expect(calls.filter((call) => call.url.endsWith('/preview'))).toHaveLength(2);
    expect(host.querySelector('#definition-pack-accept')).not.toBeNull();
  });

  it('keeps the form ready after a preview failure', async () => {
    previewReply = () => new Response(JSON.stringify({ error: 'bad signature' }), { status: 400 });
    app = mount(DefinitionPackSettings, { target: host });
    await settle();
    await fillAndPreview();

    expect(host.textContent).toContain('bad signature');
    expect(host.querySelector('#definition-pack-accept')).toBeNull();
    const preview = [...host.querySelectorAll('button')].find((button) =>
      button.textContent?.includes('Preview pack'),
    );
    expect(preview?.disabled).toBe(false);
    expect((field('definition-pack-public-key') as HTMLTextAreaElement).value).toBe('cHVibGljLWtleQ==');
  });

  it('keeps a live preview after install failure when the token is still valid', async () => {
    installReply = () => new Response(JSON.stringify({ error: 'conflict' }), { status: 409 });
    app = mount(DefinitionPackSettings, { target: host });
    await settle();
    await fillAndPreview();
    const accept = host.querySelector<HTMLInputElement>('#definition-pack-accept')!;
    accept.checked = true;
    accept.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    click('Install pack');
    await settle();

    expect(host.textContent).toContain('conflict');
    expect(host.textContent).toContain('Permission is hereby granted.');
    expect(host.querySelector('#definition-pack-accept')).not.toBeNull();
  });

  it('does not activate a zero-runnable revision', async () => {
    app = mount(DefinitionPackSettings, { target: host });
    await settle();

    const buttons = [...host.querySelectorAll('button')].filter((button) =>
      button.textContent?.includes('Activate'),
    );
    const zero = buttons.find((button) => button.closest('li')?.textContent?.includes('2026.08.01'));
    expect(zero?.disabled).toBe(true);
    expect(calls.some((call) => call.url.endsWith('/activate'))).toBe(false);
  });

  it('activates a runnable installed revision with the exact ref and shows a restart warning', async () => {
    app = mount(DefinitionPackSettings, { target: host });
    await settle();

    const activate = [...host.querySelectorAll('button')].find(
      (button) =>
        button.textContent?.includes('Activate') && button.closest('li')?.textContent?.includes('2026.08.14'),
    );
    expect(activate?.disabled).toBe(false);
    activate!.click();
    await settle();

    const sent = calls.find((call) => call.url.endsWith('/activate'));
    expect(sent?.method).toBe('POST');
    expect(sent?.body).toBe(JSON.stringify({ source: 'community', revision: '2026.08.14' }));
    expect(host.textContent).toContain('Restart required');
    expect(host.textContent).toContain('Restart Caravan to make it active');
    expect(calls.some((call) => call.url.includes('/shutdown'))).toBe(false);
    expect(calls.filter((call) => call.url.endsWith('/definition-packs') && call.method === 'GET')).toHaveLength(2);
  });

  it('rolls back only a pending revision and reloads the list', async () => {
    app = mount(DefinitionPackSettings, { target: host });
    await settle();

    const rollback = [...host.querySelectorAll('button')].find((button) =>
      button.textContent?.includes('Roll back'),
    );
    expect(rollback).toBeDefined();
    expect(rollback?.closest('li')?.textContent).toContain('2026.08.10');
    rollback!.click();
    await settle();

    const sent = calls.find((call) => call.url.endsWith('/rollback'));
    expect(sent?.body).toBe(JSON.stringify({ source: 'community', revision: '2026.08.10' }));
    expect(calls.filter((call) => call.url.endsWith('/definition-packs') && call.method === 'GET')).toHaveLength(2);
  });

  it('clears the public key from the DOM after cancel', async () => {
    app = mount(DefinitionPackSettings, { target: host });
    await settle();
    await fillAndPreview();
    expect((field('definition-pack-public-key') as HTMLTextAreaElement).value).toBe('cHVibGljLWtleQ==');
    click('Cancel');
    flushSync();
    expect((field('definition-pack-public-key') as HTMLTextAreaElement).value).toBe('');
    expect(host.textContent).not.toContain('cHVibGljLWtleQ==');
    expect(host.textContent).not.toContain('preview-token-secret');
    expect(host.querySelector('#definition-pack-accept')).toBeNull();
  });
});
