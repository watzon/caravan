/** Provider-backed scene detail: identity, metadata, availability, and request flow. */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import AdultScene from './AdultScene.svelte';
import type { SceneMeta } from '../api/types';
import { clearToasts } from '../state/toast.svelte';

function scene(extra: Partial<SceneMeta> = {}): SceneMeta {
  return {
    media_type: 'scene',
    provider: 'stashbox:tpdb',
    stash_id: 'scene/a',
    site_stash_id: 'site-a',
    site_name: 'Vixen',
    title: 'After Hours',
    code: 'ABC-123',
    overview: 'A provider-backed scene.',
    date: '2026-07-12',
    duration: 2472,
    performers: ['Sienna Vale', 'Mara Solis'],
    url: 'https://theporndb.net/scenes/scene-a',
    image_url: '/scene.jpg',
    in_library: false,
    library_id: 0,
    requested: false,
    ...extra,
  };
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

async function settle() {
  for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let served: SceneMeta;
let calls: { url: string; method: string; body: unknown }[];
let fail = false;

function open() {
  app = mount(AdultScene, {
    target: host,
    props: { provider: 'stashbox:tpdb', stashID: 'scene/a' },
  }) as Record<string, unknown>;
}

beforeEach(() => {
  served = scene();
  calls = [];
  fail = false;
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
      if (method === 'POST') return json({ id: 1, status: 'pending' }, 201);
      if (fail) return json({ error: 'metadata provider is unavailable' }, 503);
      return json(served);
    }),
  );
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  vi.unstubAllGlobals();
  clearToasts();
});

describe('AdultScene', () => {
  it('fetches its provider identity and renders provider metadata', async () => {
    open();
    await settle();

    expect(calls).toHaveLength(1);
    expect(calls[0]).toMatchObject({
      method: 'GET',
      url: '/api/v1/adult/scenes/scene%2Fa?provider=stashbox%3Atpdb',
    });
    expect(host.textContent).toContain('After Hours');
    expect(host.textContent).toContain('Vixen');
    expect(host.textContent).toContain('12 Jul 2026');
    expect(host.textContent).toContain('41:12');
    expect(host.textContent).toContain('ABC-123');
    expect(host.textContent).toContain('Sienna Vale, Mara Solis');
    expect(host.querySelector('a[href="https://theporndb.net/scenes/scene-a"]')).not.toBeNull();
    expect(host.querySelector('a[href="/discover/adult"]')).not.toBeNull();
  });

  it('reports owned and requested scenes without offering an invalid episode link or Request', async () => {
    served = scene({ in_library: true, requested: true, library_id: 9 });
    open();
    await settle();

    expect(host.textContent).toContain('IN LIBRARY');
    expect(host.textContent).not.toContain('REQUESTED');
    expect([...host.querySelectorAll('button')].some((button) => button.textContent?.trim() === 'Request')).toBe(false);
    expect(host.querySelector('a[href="/library/episodes/9"]')).toBeNull();

    unmount(app!);
    app = undefined;
    served = scene({ requested: true });
    open();
    await settle();
    expect(host.textContent).toContain('REQUESTED');
    expect([...host.querySelectorAll('button')].some((button) => button.textContent?.trim() === 'Request')).toBe(false);
  });

  it('requests an unowned scene, including provider identity, without refetching', async () => {
    open();
    await settle();
    const request = [...host.querySelectorAll<HTMLButtonElement>('button')].find(
      (button) => button.textContent?.trim() === 'Request',
    );
    request?.click();
    await settle();

    expect(calls).toHaveLength(2);
    expect(calls[1]).toMatchObject({
      method: 'POST',
      url: '/api/v1/requests',
      body: {
        media_type: 'scene',
        tmdb_id: 0,
        stash_id: 'scene/a',
        title: 'After Hours',
        year: 2026,
        poster_path: '/scene.jpg',
        provider: 'stashbox:tpdb',
      },
    });
    expect(host.textContent).toContain('REQUESTED');
    expect([...host.querySelectorAll('button')].some((button) => button.textContent?.trim() === 'Request')).toBe(false);
  });

  it('renders missing metadata honestly and retries a failed fetch', async () => {
    fail = true;
    open();
    await settle();
    expect(host.textContent).toContain('metadata provider is unavailable');

    fail = false;
    [...host.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent?.trim() === 'Retry')
      ?.click();
    await settle();

    expect(calls).toHaveLength(2);
    expect(host.textContent).toContain('After Hours');

    unmount(app!);
    app = undefined;
    served = scene({ site_name: '', code: '', overview: '', performers: [], date: '', duration: 0, url: '' });
    open();
    await settle();
    expect(host.textContent).toContain('No overview available.');
    expect(host.textContent).toContain('No performers listed.');
    expect(host.textContent).toContain('-');
  });
});
