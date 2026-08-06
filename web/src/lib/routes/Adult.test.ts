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
import type { SessionUser, Site } from '../api/types';
import { navigate } from '../router.svelte';
import { session } from '../state/session.svelte';
import { clearToasts } from '../state/toast.svelte';

type SiteStatus = 'downloaded' | 'incomplete' | 'wanted' | 'unmonitored';

function site(
  id: number,
  title: string,
  addedAt: string,
  status: SiteStatus,
): Site {
  const [sceneCount, sceneFileCount, monitored] = {
    downloaded: [2, 2, true],
    incomplete: [2, 1, true],
    wanted: [2, 0, true],
    unmonitored: [2, 0, false],
  }[status] as [number, number, boolean];

  return {
    id,
    stash_id: `site-${id}`,
    title,
    sort_title: title.toLowerCase(),
    overview: '',
    path: `Adult/${title}`,
    poster_path: '',
    poster_url: '',
    monitored,
    quality_profile_id: 0,
    library_id: 1,
    added_at: addedAt,
    updated_at: addedAt,
    scene_count: sceneCount,
    scene_file_count: sceneFileCount,
  };
}

const SITES = [
  site(40, 'Zulu Club', '2026-04-01T00:00:00Z', 'wanted'),
  site(10, 'Alpha Club', '2024-02-01T00:00:00Z', 'downloaded'),
  site(30, 'Bravo Studio', '2025-03-01T00:00:00Z', 'incomplete'),
  site(20, 'Delta House', '2023-01-01T00:00:00Z', 'unmonitored'),
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

async function mountShelf(
  role: 'admin' | 'member',
  url = '/adult',
): Promise<HTMLElement> {
  session.user = user(role);
  window.history.replaceState({}, '', url);
  navigate(url, { replace: true });
  host = document.createElement('div');
  document.body.appendChild(host);
  app = mount(Adult, { target: host, props: {} }) as Record<string, unknown>;
  flushSync();
  await vi.waitFor(() => {
    if (!host!.textContent?.includes('Zulu Club')) throw new Error('not loaded');
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

function sortTrigger(): HTMLButtonElement {
  const trigger = [
    ...host!.querySelectorAll<HTMLButtonElement>('button[aria-haspopup="dialog"]'),
  ].find((button) => (button.textContent ?? '').trim().startsWith('Sort'));
  expect(trigger, 'the site sort dropdown').toBeTruthy();
  return trigger!;
}

/** The visible option names, in the rail's order. Opens and dismisses the popover. */
function sortLabels(): string[] {
  if (!host!.querySelector('[role="dialog"][aria-label^="Sort"]')) {
    sortTrigger().click();
    flushSync();
  }
  const panel = host!.querySelector<HTMLElement>('[role="dialog"][aria-label^="Sort"]');
  const labels = [...(panel?.querySelectorAll<HTMLButtonElement>('button') ?? [])].map((button) =>
    (button.textContent ?? '').trim(),
  );
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
  flushSync();
  return labels;
}

function pickSort(name: 'Title' | 'Added' | 'Status'): void {
  if (!host!.querySelector('[role="dialog"][aria-label^="Sort"]')) {
    sortTrigger().click();
    flushSync();
  }
  const panel = host!.querySelector<HTMLElement>('[role="dialog"][aria-label^="Sort"]');
  const option = [...(panel?.querySelectorAll<HTMLButtonElement>('button') ?? [])].find(
    (button) => (button.textContent ?? '').trim() === name,
  );
  expect(option, `the "${name}" sort option`).toBeTruthy();
  option!.click();
  flushSync();
  // A pick leaves the popover open for a second one; dismiss it as a reader does.
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
  flushSync();
}

function cardTitles(): string[] {
  const links = [...host!.querySelectorAll<HTMLElement>('a[href^="/adult/sites/"]')];
  const toggles = [
    ...host!.querySelectorAll<HTMLElement>('button[aria-pressed][aria-label]'),
  ];
  return (links.length > 0 ? links : toggles).map(
    (card) => card.querySelector('p[title]')?.textContent?.trim() ?? '',
  );
}

function typeFilter(value: string): void {
  const input = host!.querySelector<HTMLInputElement>('input[type="search"]');
  expect(input, 'the site filter').toBeTruthy();
  input!.value = value;
  input!.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
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
  navigate('/', { replace: true });
});

describe('the Adult shelf grid', () => {
  it('lets an admin select sites and act on them in bulk', async () => {
    stubFetch();
    await mountShelf('admin');

    const toggle = selectToggle();
    expect(toggle, 'a card that can start a selection').toBeTruthy();
    toggle!.click();
    flushSync();

    // The shared action bar names the selected subject for assistive technology;
    // its visible label stays a bare count, while the confirm carries the noun.
    const bar = document.querySelector('[aria-label="Actions for 1 selected site"]');
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
    expect(document.querySelector('[aria-label="Actions for 1 selected site"]')).toBeNull();
    // The shelf itself still renders: reading is what the grant is for.
    expect(host!.textContent).toContain('Zulu Club');
    expect(calls.every((c) => c.method === 'GET')).toBe(true);
  });
});

describe('the Adult shelf controls', () => {
  it('labels the sort and site filter controls', async () => {
    stubFetch();
    await mountShelf('admin');

    expect(sortTrigger().textContent?.trim()).toBe('Sort: Title');
    expect(
      host!.querySelector<HTMLInputElement>('input[type="search"]')?.getAttribute('aria-label'),
    ).toBe('Filter sites by name');
  });
});

describe('the Adult shelf sort', () => {
  it.each([
    ['title', ['Alpha Club', 'Bravo Studio', 'Delta House', 'Zulu Club']],
    ['added', ['Zulu Club', 'Bravo Studio', 'Alpha Club', 'Delta House']],
    ['status', ['Alpha Club', 'Bravo Studio', 'Zulu Club', 'Delta House']],
  ] as const)('reads the supported %s sort from the URL', async (sort, expected) => {
    stubFetch();
    await mountShelf('admin', `/adult?sort=${sort}`);

    expect(sortTrigger().textContent?.trim()).toBe(
      `Sort: ${{ title: 'Title', added: 'Added', status: 'Status' }[sort]}`,
    );
    expect(sortLabels()).toEqual(['Title', 'Added', 'Status']);
    expect(cardTitles()).toEqual(expected);
    expect(calls).toEqual([{ url: '/api/v1/adult/sites', method: 'GET' }]);
  });

  it('falls back to title for an invalid URL value and removes it as the default', async () => {
    stubFetch();
    await mountShelf('admin', '/adult?view=grid&sort=recent#sites');

    expect(sortTrigger().textContent?.trim()).toBe('Sort: Title');
    expect(cardTitles()).toEqual(['Alpha Club', 'Bravo Studio', 'Delta House', 'Zulu Club']);

    pickSort('Title');
    expect(`${window.location.pathname}${window.location.search}${window.location.hash}`).toBe(
      '/adult?view=grid#sites',
    );
    expect(calls).toEqual([{ url: '/api/v1/adult/sites', method: 'GET' }]);
  });

  it('preserves unrelated query state and the fragment without reloading from the backend', async () => {
    stubFetch();
    await mountShelf('member', '/adult?view=grid&sort=status#sites');

    pickSort('Added');
    expect(`${window.location.pathname}${window.location.search}${window.location.hash}`).toBe(
      '/adult?view=grid&sort=added#sites',
    );

    pickSort('Title');
    expect(`${window.location.pathname}${window.location.search}${window.location.hash}`).toBe(
      '/adult?view=grid#sites',
    );
    expect(calls).toEqual([{ url: '/api/v1/adult/sites', method: 'GET' }]);
  });

  it('sorts a filtered copy and keeps the selection through filter and sort changes', async () => {
    stubFetch();
    await mountShelf('admin', '/adult?sort=added');

    typeFilter('club');
    expect(cardTitles()).toEqual(['Zulu Club', 'Alpha Club']);

    const toggle = host!.querySelector<HTMLButtonElement>(
      'button[aria-label^="Select Zulu Club, "]',
    );
    expect(toggle, 'the Zulu Club selection control').toBeTruthy();
    toggle!.click();
    flushSync();

    typeFilter('studio');
    expect(cardTitles()).toEqual(['Bravo Studio']);
    expect(host!.textContent).toContain('1 selected');

    typeFilter('club');
    pickSort('Status');
    expect(cardTitles()).toEqual(['Alpha Club', 'Zulu Club']);
    expect(
      host!.querySelector('button[aria-label^="Zulu Club, "][aria-pressed="true"]'),
      'the selected site after sorting',
    ).toBeTruthy();
    expect(calls).toEqual([{ url: '/api/v1/adult/sites', method: 'GET' }]);
  });
});

/**
 * The Scenes tab is retired (PLAN phase 12 task 4). Browsing the provider's
 * catalogue moved to Explore, beside the other two catalogues, so the shelf is
 * what Caravan HOLDS and nothing else — and a tab strip with one tab in it is a
 * strip that says nothing, so there is no strip at all.
 */
describe('the retired Scenes tab', () => {
  it('leaves the shelf with no tab strip and no link to the old route', async () => {
    stubFetch();
    await mountShelf('admin');

    expect(host!.querySelector('[role="tablist"]')).toBeNull();
    expect(host!.textContent).not.toContain('Scenes');
    expect(host!.querySelector('a[href="/adult/scenes"]')).toBeNull();
  });
});
