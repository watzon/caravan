/**
 * A site's page, brought to parity with SeriesDetail's actions.
 *
 * The two properties under test are the actions themselves — search the site,
 * open the picker at each level, link out to the provider — and who is offered
 * them: every one is an admin write, on a page a granted MEMBER can read, so a
 * member must see the same information and none of the buttons.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import AdultSite from './AdultSite.svelte';
import type { SessionUser } from '../api/types';
import { session } from '../state/session.svelte';
import { clearToasts } from '../state/toast.svelte';

interface Call {
  url: string;
  method: string;
  body: Record<string, unknown> | null;
}

const SITE = {
  id: 7,
  stash_id: 'e3b61b3e-1111-4111-8111-111111111111',
  title: 'Brazzers',
  overview: '',
  path: 'Adult/Brazzers',
  poster_path: '',
  poster_url: '',
  monitored: true,
  scene_count: 3,
  scene_file_count: 1,
  added_at: '2024-01-01T00:00:00Z',
  provider_url: 'https://theporndb.net/sites/e3b61b3e-1111-4111-8111-111111111111',
  years: [
    {
      year: 2022,
      monitored: true,
      scenes: [
        {
          id: 11,
          series_id: 7,
          year: 2022,
          number: 3,
          stash_id: 'scene-3',
          title: 'Deep Impact',
          overview: '',
          studio: 'Brazzers',
          performers: ['Jane Doe'],
          url: 'https://www.brazzers.com/scene/deep-impact',
          release_date: '2022-03-14T00:00:00Z',
          monitored: true,
          file: null,
        },
        {
          // A big cast and a file: the performers column summarises, and the
          // quality rides with the status.
          id: 13,
          series_id: 7,
          year: 2022,
          number: 5,
          stash_id: 'scene-5',
          title: 'Crowd Scene',
          overview: '',
          studio: 'Brazzers',
          performers: ['Ava Wells', 'Ivy Rain', 'Mia Stone', 'Nina Reed'],
          url: '',
          release_date: '2022-05-14T00:00:00Z',
          monitored: true,
          file: {
            id: 99,
            path: 'Adult/Brazzers/2022/scene.mkv',
            size: 1024,
            movie_id: 0,
            quality: '1080p',
            source: 'webdl',
            codec: 'h264',
            audio: 'aac',
            release_group: 'GROUP',
            added_at: '2024-01-01T00:00:00Z',
            modified_at: '2024-01-01T00:00:00Z',
            compatibility: { verdict: 'compatible', reasons: [] },
          },
        },
        {
          // No url: the link is offered only where the provider stored one.
          id: 12,
          series_id: 7,
          year: 2022,
          number: 4,
          stash_id: 'scene-4',
          title: 'Shallow Impact',
          overview: '',
          studio: 'Brazzers',
          performers: [],
          url: '',
          release_date: '2022-04-14T00:00:00Z',
          monitored: true,
          file: null,
        },
      ],
    },
  ],
};

let host: HTMLElement | undefined;
let app: Record<string, unknown> | undefined;
let calls: Call[] = [];

function user(role: 'admin' | 'member'): SessionUser {
  return { username: 'someone', role, open: false, adult: true };
}

function stubFetch(site: unknown = SITE): void {
  calls = [];
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
      if (method === 'PATCH' || method === 'DELETE') {
        return new Response(null, { status: 204 });
      }
      const payload = method === 'POST' ? { queued: 4 } : site;
      return new Response(JSON.stringify(payload), {
        status: method === 'POST' ? 202 : 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
}

async function mountSite(role: 'admin' | 'member' = 'admin'): Promise<HTMLElement> {
  session.user = user(role);
  host = document.createElement('div');
  document.body.appendChild(host);
  app = mount(AdultSite, { target: host, props: { id: 7 } }) as Record<string, unknown>;
  flushSync();
  // onMount's load: let the stubbed fetch resolve before asserting on the DOM.
  await vi.waitFor(() => {
    if (!host!.textContent?.includes('Brazzers')) throw new Error('not loaded');
  });
  flushSync();
  return host;
}

function links(): HTMLAnchorElement[] {
  return [...host!.querySelectorAll('a')];
}

function hrefs(): string[] {
  return links().map((a) => a.getAttribute('href') ?? '');
}

function buttonLabelled(text: string): HTMLButtonElement | undefined {
  return [...host!.querySelectorAll('button')].find((b) => b.textContent?.includes(text));
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

describe('AdultSite actions', () => {
  it('searches every monitored scene from the header', async () => {
    stubFetch();
    await mountSite();

    const button = buttonLabelled('Search monitored');
    expect(button, 'a site-wide search action').toBeTruthy();
    button!.click();
    await vi.waitFor(() => {
      if (!calls.some((c) => c.method === 'POST')) throw new Error('no search yet');
    });

    const post = calls.find((c) => c.method === 'POST');
    // A site is a series row, and this is the same route SeriesDetail uses.
    expect(post?.url).toBe('/api/v1/library/series/7/search');
  });

  it('offers the picker at the site, year and scene levels', async () => {
    stubFetch();
    await mountSite();

    expect(hrefs()).toContain('/adult/sites/7/search');
    expect(hrefs()).toContain('/adult/sites/7/search/2022');
    // The scene's own number within its year, which is how the row names it.
    expect(hrefs()).toContain('/adult/sites/7/search/2022/3');
    expect(hrefs()).toContain('/adult/sites/7/search/2022/4');
  });

  it('links the provider id out to the endpoint the server named', async () => {
    stubFetch();
    await mountSite();

    const link = links().find((a) => a.getAttribute('href') === SITE.provider_url);
    expect(link, 'the provider id as a link').toBeTruthy();
    expect(link!.textContent).toContain(SITE.stash_id);
    expect(link!.getAttribute('target')).toBe('_blank');
    expect(link!.getAttribute('rel')).toBe('noopener noreferrer');
  });

  it('renders the provider id as plain text when there is no page for it', async () => {
    stubFetch({ ...SITE, provider_url: '' });
    await mountSite();

    expect(host!.textContent).toContain(SITE.stash_id);
    expect(hrefs().some((href) => href.includes('theporndb.net'))).toBe(false);
  });

  it('links a scene to its own page, and only when one is stored', async () => {
    stubFetch();
    await mountSite();

    const scene = links().find(
      (a) => a.getAttribute('href') === 'https://www.brazzers.com/scene/deep-impact',
    );
    expect(scene, 'the scene title as a link').toBeTruthy();
    expect(scene!.getAttribute('rel')).toBe('noopener noreferrer');
    // The second scene has no url, so its title is text — one link, not two.
    expect(host!.textContent).toContain('Shallow Impact');
    expect(links().filter((a) => a.getAttribute('href')?.includes('brazzers.com/scene'))).toHaveLength(1);
  });
});

describe('AdultSite for a granted member', () => {
  it('offers no action a member would be refused', async () => {
    stubFetch();
    await mountSite('member');

    expect(buttonLabelled('Search monitored')).toBeUndefined();
    expect(hrefs().some((href) => href.includes('/search'))).toBe(false);
    // Nothing was written, and nothing was even asked for beyond the page load.
    expect(calls.every((c) => c.method === 'GET')).toBe(true);
  });

  it('still shows everything that reports state', async () => {
    stubFetch();
    await mountSite('member');

    // The counts, the year, the scene rows and the provider link are all reads:
    // a member should see what will happen next, just not be able to start it.
    expect(host!.textContent).toContain('1 / 3 scenes');
    expect(host!.textContent).toContain('2022');
    expect(host!.textContent).toContain('Deep Impact');
    expect(hrefs()).toContain(SITE.provider_url);
  });
});

describe('AdultSite scene rows', () => {
  function headers(): string[] {
    return [...host!.querySelectorAll('th')].map((th) => th.textContent?.trim() ?? '');
  }

  it('gives performers a column and drops quality and size', async () => {
    stubFetch();
    await mountSite();

    const columns = headers();
    expect(columns).toContain('Performers');
    expect(columns).not.toContain('Quality');
    expect(columns).not.toContain('Size');
    // What is left is the scene, who is in it, when it came out, where it
    // stands — and, for an admin, what it is watching for and the way to go
    // looking for it.
    expect(columns).toEqual([
      'Scene',
      'Performers',
      'Released',
      'Status',
      'Monitored',
      'Search',
    ]);
  });

  it('summarises a big cast and keeps the whole list on hover', async () => {
    stubFetch();
    await mountSite();

    const cell = [...host!.querySelectorAll('td')].find((td) =>
      td.textContent?.includes('Ava Wells'),
    );
    expect(cell, 'a performers cell').toBeTruthy();
    // Two names then a count: the column holds two at this density.
    expect(cell!.textContent?.trim()).toBe('Ava Wells, Ivy Rain +2');
    expect(cell!.getAttribute('title')).toBe('Ava Wells, Ivy Rain, Mia Stone, Nina Reed');
  });

  it('keeps the performers out of the title cell', async () => {
    stubFetch();
    await mountSite();

    const link = [...host!.querySelectorAll('a')].find(
      (a) => a.getAttribute('href') === 'https://www.brazzers.com/scene/deep-impact',
    );
    // The title is the title now — the performers moved to their own column.
    expect(link!.textContent?.trim()).toBe('Deep Impact');
  });

  it("keeps a downloaded scene's quality beside its status", async () => {
    stubFetch();
    await mountSite();

    const row = [...host!.querySelectorAll('tr')].find((tr) =>
      tr.textContent?.includes('Crowd Scene'),
    );
    expect(row, 'the row for the scene with a file').toBeTruthy();
    expect(row!.textContent).toContain('1080p');
  });
});

describe('AdultSite monitoring and removal', () => {
  /**
   * The switch whose accessible name is `label` — the per-year and per-scene
   * toggles, which are still switches.
   */
  function toggle(label: string): HTMLElement | undefined {
    return [...host!.querySelectorAll<HTMLElement>('[role="switch"]')].find(
      (el) => el.getAttribute('aria-label') === label || el.textContent?.trim() === label,
    );
  }

  /**
   * The header's monitored control, which is now an icon toggle button rather
   * than a labeled switch: it reports state with aria-pressed and names the
   * ACTION, not the state, so the label says what a click will do.
   */
  function monitorButton(): HTMLElement | undefined {
    return [...host!.querySelectorAll<HTMLElement>('button[aria-pressed]')].find((el) =>
      el.getAttribute('aria-label')?.includes('monitor'),
    );
  }

  /** The header's ⋯ trigger. */
  function menuTrigger(): HTMLElement | undefined {
    return [...host!.querySelectorAll<HTMLElement>('button')].find(
      (el) => el.getAttribute('aria-label') === 'More actions for Brazzers',
    );
  }

  /** The Remove item, with its menu opened. */
  function removeItem(): HTMLElement | undefined {
    menuTrigger()?.click();
    flushSync();
    return [...host!.querySelectorAll<HTMLElement>('[role="menuitem"]')].find((el) =>
      el.textContent?.includes('Remove'),
    );
  }

  async function flip(label: string) {
    const control = label === 'Monitored' ? monitorButton() : toggle(label);
    expect(control, `a control for ${label}`).toBeTruthy();
    control!.click();
    await vi.waitFor(() => {
      if (!calls.some((c) => c.method === 'PATCH')) throw new Error('no write yet');
    });
  }

  it('turns the whole site off through the series route', async () => {
    stubFetch();
    await mountSite();

    await flip('Monitored');
    const patch = calls.find((c) => c.method === 'PATCH');
    expect(patch?.url).toBe('/api/v1/library/series/7');
    expect(patch?.body).toEqual({ monitored: false });
  });

  it('turns one release year off through the season route', async () => {
    stubFetch();
    await mountSite();

    // A year IS a season; 2022 is both its label and its season number.
    await flip('Monitor 2022');
    const patch = calls.find((c) => c.method === 'PATCH');
    expect(patch?.url).toBe('/api/v1/library/series/7/seasons/2022');
    expect(patch?.body).toEqual({ monitored: false });
  });

  it('turns one scene off through the episode route', async () => {
    stubFetch();
    await mountSite();

    await flip('Monitor #003');
    const patch = calls.find((c) => c.method === 'PATCH');
    expect(patch?.url).toBe('/api/v1/library/episodes/11');
    expect(patch?.body).toEqual({ monitored: false });
  });

  it('reloads after a write rather than guessing what cascaded', async () => {
    stubFetch();
    await mountSite();
    const before = calls.filter((c) => c.method === 'GET').length;

    await flip('Monitored');
    await vi.waitFor(() => {
      if (calls.filter((c) => c.method === 'GET').length <= before) {
        throw new Error('no reload yet');
      }
    });
  });

  it('removes the site without its files and goes back to the shelf', async () => {
    stubFetch();
    await mountSite();

    removeItem()!.click();
    flushSync();
    const confirm = [...host!.querySelectorAll('button')].find(
      (b) => b.textContent?.trim() === 'Remove',
    );
    expect(confirm, 'the confirm button').toBeTruthy();
    confirm!.click();
    await vi.waitFor(() => {
      if (!calls.some((c) => c.method === 'DELETE')) throw new Error('no delete yet');
    });

    const del = calls.find((c) => c.method === 'DELETE');
    // No files=true: untracking leaves the media alone, which is the default.
    expect(del?.url).toBe('/api/v1/library/series/7');
    await vi.waitFor(() => {
      if (window.location.pathname !== '/adult') throw new Error('did not navigate');
    });
  });

  it('removes the site with its files when the box is checked', async () => {
    stubFetch();
    await mountSite();

    removeItem()!.click();
    flushSync();
    // The count comes off the detail response, so the confirm names it.
    expect(host!.textContent).toContain('Also delete 1 file from disk');

    const box = [...host!.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')].at(-1)!;
    box.checked = true;
    box.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    [...host!.querySelectorAll('button')]
      .find((b) => b.textContent?.trim() === 'Remove')!
      .click();
    await vi.waitFor(() => {
      if (!calls.some((c) => c.method === 'DELETE')) throw new Error('no delete yet');
    });

    expect(calls.find((c) => c.method === 'DELETE')?.url).toBe(
      '/api/v1/library/series/7?files=true',
    );
  });

  it('closes the menu on Escape without removing anything', async () => {
    stubFetch();
    await mountSite();

    menuTrigger()!.click();
    flushSync();
    expect(host!.querySelector('[role="menu"]'), 'the menu is open').not.toBeNull();

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    flushSync();
    expect(host!.querySelector('[role="menu"]')).toBeNull();
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
  });

  it('offers a member no control over any of it', async () => {
    stubFetch();
    await mountSite('member');

    expect(monitorButton()).toBeUndefined();
    expect(menuTrigger()).toBeUndefined();
    expect(host!.querySelector('[role="menu"]')).toBeNull();
    expect(toggle('Monitor 2022')).toBeUndefined();
    expect(toggle('Monitor #003')).toBeUndefined();
    // But the state itself is still readable: an unmonitored year says so.
    expect(host!.textContent).toContain('2022');
  });
});
