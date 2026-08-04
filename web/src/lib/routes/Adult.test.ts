/**
 * The Adult shelf's grid selection, which is the Series shelf's: same cards,
 * same action bar, same three routes — a site is a series row.
 *
 * What is worth asserting here is the gate, not the bar's behaviour (Series
 * already covers that): every action behind a selection is a write a member's
 * session is refused, so a member must not be able to start one.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Adult from './Adult.svelte';
import type { SessionUser } from '../api/types';
import { session } from '../state/session.svelte';
import { clearToasts } from '../state/toast.svelte';

const SITES = [
  {
    id: 7,
    stash_id: 'site-1',
    title: 'Brazzers',
    path: 'Adult/Brazzers',
    poster_path: '',
    poster_url: '',
    monitored: true,
    scene_count: 2,
    scene_file_count: 1,
    added_at: '2024-01-01T00:00:00Z',
  },
];

let host: HTMLElement | undefined;
let app: Record<string, unknown> | undefined;
let calls: { url: string; method: string }[] = [];

function user(role: 'admin' | 'member'): SessionUser {
  return { username: 'someone', role, open: false, adult: true };
}

function stubFetch(): void {
  calls = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      calls.push({ url, method });
      if (method !== 'GET') return new Response(null, { status: 204 });
      return new Response(JSON.stringify({ sites: SITES }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
}

async function mountShelf(role: 'admin' | 'member'): Promise<HTMLElement> {
  session.user = user(role);
  host = document.createElement('div');
  document.body.appendChild(host);
  app = mount(Adult, { target: host, props: {} }) as Record<string, unknown>;
  flushSync();
  await vi.waitFor(() => {
    if (!host!.textContent?.includes('Brazzers')) throw new Error('not loaded');
  });
  flushSync();
  return host;
}

/** The check circle a card offers to start a selection with. */
function selectToggle(): HTMLElement | undefined {
  return [...host!.querySelectorAll<HTMLElement>('button')].find((b) =>
    b.getAttribute('aria-label')?.startsWith('Select '),
  );
}

afterEach(() => {
  if (app) unmount(app);
  host?.remove();
  app = undefined;
  host = undefined;
  session.user = null;
  clearToasts();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('the Adult shelf grid', () => {
  it('lets an admin select sites and act on them in bulk', async () => {
    stubFetch();
    await mountShelf('admin');

    const toggle = selectToggle();
    expect(toggle, 'a card that can start a selection').toBeTruthy();
    toggle!.click();
    flushSync();

    // The shared action bar. Its own label is a bare count; the shelf's nouns
    // appear on the remove confirm, which is where they matter.
    const bar = document.querySelector('[aria-label="Selection actions"]');
    expect(bar, 'the selection action bar').toBeTruthy();
    expect(bar!.textContent).toContain('1 selected');
    for (const label of ['Search', 'Monitor', 'Unmonitor', 'Remove…']) {
      expect(bar!.textContent).toContain(label);
    }
  });

  it('offers a member nothing to select with', async () => {
    stubFetch();
    await mountShelf('member');

    expect(selectToggle()).toBeUndefined();
    expect(document.querySelector('[aria-label="Selection actions"]')).toBeNull();
    // The shelf itself still renders: reading is what the grant is for.
    expect(host!.textContent).toContain('Brazzers');
    expect(calls.every((c) => c.method === 'GET')).toBe(true);
  });
});
