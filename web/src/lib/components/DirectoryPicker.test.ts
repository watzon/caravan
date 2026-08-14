/**
 * DirectoryPicker — browse the host filesystem to choose a folder.
 *
 * The text field stays the source of truth so a Docker user can still type
 * /data, and so existing first-run tests that fill #storage-root keep working.
 * Browse is the other way in: it lists folders on the machine running Caravan,
 * not the browser's disk.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import DirectoryPicker from './DirectoryPicker.svelte';
import type { DirectoryListing } from '../api/types';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function listing(path: string, parent: string, names: string[]): DirectoryListing {
  return {
    path,
    parent,
    directories: names.map((name) => ({ name, path: `${path === '/' ? '' : path}/${name}` })),
  };
}

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let listings: Record<string, DirectoryListing | { error: string; status: number }>;
let calls: string[];

beforeEach(() => {
  calls = [];
  listings = {
    '': listing('/', '', ['data', 'home']),
    '/data': listing('/data', '/', ['media', 'downloads']),
    '/data/media': listing('/data/media', '/data', []),
  };
  host = document.createElement('div');
  document.body.appendChild(host);

  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      calls.push(url);
      const path = new URL(url, 'http://caravan.test').searchParams.get('path') ?? '';
      const body = listings[path];
      if (!body) throw new Error(`unexpected directory listing: ${url}`);
      if ('status' in body) return jsonResponse({ error: body.error }, body.status);
      return jsonResponse(body);
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
  await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

function mountPicker(value = '') {
  const props = { value };
  app = mount(DirectoryPicker, {
    target: host,
    props: {
      id: 'storage-root',
      get value() {
        return props.value;
      },
      set value(next: string) {
        props.value = next;
      },
      placeholder: '/data',
    },
  });
  flushSync();
  return props;
}

function input(): HTMLInputElement {
  const el = host.querySelector<HTMLInputElement>('#storage-root');
  expect(el, '#storage-root').not.toBeNull();
  return el!;
}

function button(label: string): HTMLButtonElement {
  const found = [...host.querySelectorAll('button')].find((candidate) =>
    candidate.textContent?.includes(label),
  );
  expect(found, `button labelled ${label}`).toBeDefined();
  return found as HTMLButtonElement;
}

describe('DirectoryPicker', () => {
  it('keeps the typed value on the labelled input without talking to the host', () => {
    const props = mountPicker('/existing');

    expect(input().value).toBe('/existing');
    expect(calls).toEqual([]);

    input().value = '/data';
    input().dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    expect(props.value).toBe('/data');
    expect(calls).toEqual([]);
  });

  it('opens at the filesystem root when the field is empty', async () => {
    mountPicker();
    button('Browse').click();
    await settle();

    expect(calls).toEqual(['/api/v1/system/directories']);
    expect(host.textContent).toContain('data');
    expect(host.textContent).toContain('home');
  });

  it('opens at the typed path when it exists', async () => {
    mountPicker('/data');
    button('Browse').click();
    await settle();

    expect(calls).toEqual(['/api/v1/system/directories?path=%2Fdata']);
    expect(host.textContent).toContain('media');
    expect(host.textContent).toContain('downloads');
  });

  it('descends into a child folder and uses it as the value', async () => {
    const props = mountPicker('/data');
    button('Browse').click();
    await settle();

    button('media').click();
    await settle();
    button('Use this folder').click();
    flushSync();

    expect(props.value).toBe('/data/media');
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });

  it('walks up to the parent folder', async () => {
    mountPicker('/data/media');
    listings['/data/media'] = listing('/data/media', '/data', []);
    button('Browse').click();
    await settle();

    button('Up').click();
    await settle();

    expect(calls.at(-1)).toBe('/api/v1/system/directories?path=%2Fdata');
    expect(host.textContent).toContain('media');
  });

  it('walks from a Windows volume back to the volume list', async () => {
    listings['C:\\'] = {
      path: 'C:\\',
      parent: '',
      directories: [{ name: 'Media', path: 'C:\\Media' }],
    };
    mountPicker('C:\\');
    button('Browse').click();
    await settle();

    button('Up').click();
    await settle();

    expect(calls.at(-1)).toBe('/api/v1/system/directories');
    expect(host.textContent).toContain('data');
  });

  it('falls back to the filesystem root when the typed path cannot be listed', async () => {
    listings['/missing'] = { error: 'directory does not exist', status: 400 };
    mountPicker('/missing');
    button('Browse').click();
    await settle();

    expect(calls).toEqual([
      '/api/v1/system/directories?path=%2Fmissing',
      '/api/v1/system/directories',
    ]);
    expect(host.textContent).toContain('data');
    expect(host.textContent).toContain('home');
  });
});
