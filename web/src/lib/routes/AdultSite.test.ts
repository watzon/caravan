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
import { CATALOGUING_POLL_MS } from '../adult';
import type { SessionUser, SystemStatus } from '../api/types';
import { session } from '../state/session.svelte';
import { system } from '../state/system.svelte';
import { applyInvalidation } from '../state/live';
import { clearToasts } from '../state/toast.svelte';

interface Call {
  url: string;
  method: string;
  body: Record<string, unknown> | null;
}

const STATUS: SystemStatus = {
  version: '0.1.0',
  mode: 'server',
  storage_root: '/data',
  schema_version: 14,
  scanning: false,
  counts: { movies: 0, series: 0, media_files: 1, unmatched: 0 },
  disk_free_bytes: 1,
  disk_total_bytes: 2,
  engine_health: 'ok',
  ffmpeg_available: true,
};

const SITE = {
  id: 7,
  stash_id: 'e3b61b3e-1111-4111-8111-111111111111',
  title: 'Brazzers',
  overview: '',
  path: 'Adult/Brazzers',
  poster_path: '',
  poster_url: '',
  monitored: true,
  quality_profile_id: 0,
  library_id: 4,
  scene_count: 3,
  scene_file_count: 1,
  added_at: '2024-01-01T00:00:00Z',
  provider_url: 'https://theporndb.net/sites/e3b61b3e-1111-4111-8111-111111111111',
  cataloguing: false,
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
          provider_url: 'https://theporndb.net/scenes/scene-3',
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
          provider_url: 'https://theporndb.net/scenes/scene-5',
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
            compatibility: {
              verdict: 'incompatible',
              reasons: ['HEVC video (target allows H.264)'],
            },
          },
        },
        {
          // No provider page: the link is offered only where there is one.
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
          provider_url: '',
          release_date: '2022-04-14T00:00:00Z',
          monitored: true,
          file: null,
        },
      ],
    },
  ],
};

const COMPLETE_SITE = {
  ...SITE,
  scene_file_count: 3,
  years: SITE.years.map((year) => ({
    ...year,
    scenes: year.scenes.map((scene) => ({
      ...scene,
      file: scene.file ?? SITE.years[0]!.scenes[1]!.file,
    })),
  })),
};

function cascadeSiteMonitored<T extends typeof SITE>(site: T, monitored: boolean): T {
  return {
    ...site,
    monitored,
    years: site.years.map((year) => ({
      ...year,
      monitored,
      scenes: year.scenes.map((scene) => ({ ...scene, monitored })),
    })),
  };
}

const MIXED_SITE = {
  ...SITE,
  monitored: false,
  years: SITE.years.map((year) => ({
    ...year,
    monitored: false,
    scenes: year.scenes.map((scene, index) => ({
      ...scene,
      monitored: index === 0,
    })),
  })),
};

const PROFILES = [
  {
    id: 1,
    name: 'Adult Archive',
    is_default: false,
    tv_profile: 'capable',
  },
  {
    id: 2,
    name: 'System Standard',
    is_default: true,
    tv_profile: 'safe',
  },
];

const LIBRARIES = [
  {
    id: 4,
    kind: 'adult',
    name: 'Adult',
    active: true,
    is_default: true,
    quality_profile_id: 1,
  },
];

let host: HTMLElement | undefined;
let app: Record<string, unknown> | undefined;
let calls: Call[] = [];

function user(role: 'admin' | 'member'): SessionUser {
  return { username: 'someone', role, open: false, adult: true };
}

function stubFetch(site: unknown = SITE, queued = 4): void {
  let current = site as typeof SITE;
  calls = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      const body = typeof init?.body === 'string' ? JSON.parse(init.body) : null;
      calls.push({
        url,
        method,
        body,
      });
      if (url.endsWith('/quality-profiles')) {
        return new Response(JSON.stringify({ profiles: PROFILES }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (url.endsWith('/libraries')) {
        return new Response(JSON.stringify({ libraries: LIBRARIES }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (
        method === 'PATCH' &&
        url.endsWith('/library/series/7') &&
        body?.monitored !== undefined &&
        body?.quality_profile_id === undefined
      ) {
        current = cascadeSiteMonitored(current, Boolean(body.monitored));
        return new Response(JSON.stringify(current), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (method === 'PATCH' || method === 'DELETE') {
        return new Response(null, { status: 204 });
      }
      const payload = method === 'POST' ? { queued } : current;
      return new Response(JSON.stringify(payload), {
        status: method === 'POST' ? 202 : 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
}

async function mountSite(
  role: 'admin' | 'member' = 'admin',
  ffmpeg = true,
): Promise<HTMLElement> {
  session.user = user(role);
  system.status = { ...STATUS, ffmpeg_available: ffmpeg };
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
  system.status = null;
  clearToasts();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('AdultSite actions', () => {
  it('puts site-wide acquisition actions above the scene inventory', async () => {
    stubFetch();
    await mountSite();

    const inventory = host!.querySelector('section[aria-labelledby="adult-scenes-heading"]');
    expect(inventory).not.toBeNull();

    const button = buttonLabelled('Search monitored');
    expect(button, 'a site-wide search action').toBeTruthy();
    expect(inventory?.contains(button!)).toBe(true);

    const picker = host!.querySelector('a[href="/adult/sites/7/search"]');
    expect(picker?.textContent).toContain('Choose a release');
    expect(inventory?.contains(picker)).toBe(true);

    button!.click();
    await vi.waitFor(() => {
      if (!calls.some((c) => c.method === 'POST')) throw new Error('no search yet');
    });

    const post = calls.find((c) => c.method === 'POST');
    // A site is a series row, and this is the same route SeriesDetail uses.
    expect(post?.url).toBe('/api/v1/library/series/7/search');
  });

  it('searches monitored scenes without turning the rest on', async () => {
    stubFetch(MIXED_SITE, 1);
    await mountSite();

    const searchMonitored = buttonLabelled('Search monitored');
    expect(searchMonitored).toBeDefined();
    searchMonitored!.click();
    await vi.waitFor(() => {
      if (!calls.some((call) => call.method === 'POST')) throw new Error('no search yet');
    });

    expect(calls.filter((call) => call.method === 'PATCH' || call.method === 'POST')).toEqual([
      {
        url: '/api/v1/library/series/7/search',
        method: 'POST',
        body: null,
      },
    ]);
    expect(
      host!.querySelector('button[role="switch"][aria-label="Monitor #004"]')?.getAttribute(
        'aria-checked',
      ),
    ).toBe('false');
  });

  it('monitors every year and scene before starting the automatic search', async () => {
    stubFetch(MIXED_SITE, 2);
    await mountSite();

    expect(buttonLabelled('Search monitored')).toBeDefined();
    const monitorAndSearch = buttonLabelled('Monitor and search');
    expect(monitorAndSearch).toBeDefined();

    monitorAndSearch!.click();
    await vi.waitFor(() => {
      if (calls.filter((call) => call.method === 'PATCH' || call.method === 'POST').length < 2) {
        throw new Error('monitor and search did not finish');
      }
    });

    expect(calls.filter((call) => call.method === 'PATCH' || call.method === 'POST')).toEqual([
      {
        url: '/api/v1/library/series/7',
        method: 'PATCH',
        body: { monitored: true },
      },
      {
        url: '/api/v1/library/series/7/search',
        method: 'POST',
        body: null,
      },
    ]);
    const patchAt = calls.findIndex((call) => call.method === 'PATCH');
    expect(
      calls
        .slice(patchAt + 1)
        .some((call) => call.method === 'GET' && call.url.endsWith('/adult/sites/7')),
    ).toBe(true);
    expect(
      host!.querySelector('button[role="switch"][aria-label="Monitor #004"]')?.getAttribute(
        'aria-checked',
      ),
    ).toBe('true');
    expect(buttonLabelled('Monitor and search')).toBeUndefined();
  });

  it('does not restore old switches when a slower library reload finishes later', async () => {
    let current = MIXED_SITE;
    let cascadeDone = false;
    let releaseStale = () => {};
    const staleHold = new Promise<void>((resolve) => {
      releaseStale = resolve;
    });
    let staleGets = 0;

    calls = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = init?.method ?? 'GET';
        const body = typeof init?.body === 'string' ? JSON.parse(init.body) : null;
        calls.push({ url, method, body });
        if (url.endsWith('/quality-profiles')) {
          return new Response(JSON.stringify({ profiles: PROFILES }), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        if (url.endsWith('/libraries')) {
          return new Response(JSON.stringify({ libraries: LIBRARIES }), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        if (url.includes('/auth/me') || url.includes('/system/status')) {
          return new Response(JSON.stringify({ error: 'not this stub' }), {
            status: 500,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        if (
          method === 'PATCH' &&
          url.endsWith('/library/series/7') &&
          body?.monitored !== undefined &&
          body?.quality_profile_id === undefined
        ) {
          // Same order as the server: note the library, then cascade. The live
          // stream starts a GET that still sees the old flags.
          applyInvalidation('library');
          current = cascadeSiteMonitored(current, true);
          cascadeDone = true;
          return new Response(JSON.stringify(current), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        if (method === 'POST') {
          return new Response(JSON.stringify({ queued: 2 }), {
            status: 202,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        if (url.includes('/adult/sites/7') && !cascadeDone) {
          staleGets += 1;
          if (staleGets > 1) {
            await staleHold;
            return new Response(JSON.stringify(MIXED_SITE), {
              status: 200,
              headers: { 'Content-Type': 'application/json' },
            });
          }
        }
        return new Response(JSON.stringify(current), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }),
    );

    await mountSite();
    const monitorAndSearch = buttonLabelled('Monitor and search');
    expect(monitorAndSearch).toBeDefined();
    monitorAndSearch!.click();
    await vi.waitFor(() => {
      if (!calls.some((call) => call.method === 'POST')) throw new Error('search not queued');
    });
    expect(
      host!.querySelector('button[role="switch"][aria-label="Monitor #004"]')?.getAttribute(
        'aria-checked',
      ),
    ).toBe('true');

    releaseStale();
    await vi.waitFor(() => {
      if (staleGets < 2) throw new Error('stale reload has not finished');
    });
    await Promise.resolve();
    flushSync();
    expect(
      host!.querySelector('button[role="switch"][aria-label="Monitor #004"]')?.getAttribute(
        'aria-checked',
      ),
    ).toBe('true');
  });

  it('keeps monitored search available while a download is in flight', async () => {
    const downloading = {
      ...SITE,
      years: SITE.years.map((year) => ({
        ...year,
        scenes: year.scenes.map((scene) =>
          scene.id === 11 ? { ...scene, downloading: true } : scene,
        ),
      })),
    };
    stubFetch(downloading);
    await mountSite();

    expect(buttonLabelled('Search monitored')).toBeDefined();
    expect(host!.querySelector('a[href="/adult/sites/7/search"]')?.textContent).toContain(
      'Choose a release',
    );
    const queueLinks = host!.querySelectorAll('a[href="/queue"]');
    expect(queueLinks.length).toBeGreaterThanOrEqual(2);
    expect(queueLinks[0]?.textContent).toContain('View queue');
  });

  it('offers an explicit replacement search when every scene is imported', async () => {
    stubFetch(COMPLETE_SITE);
    await mountSite();

    expect(host!.querySelector('a[href="/adult/sites/7/search"]')?.textContent).toContain(
      'Choose another release',
    );
    expect(buttonLabelled('Search monitored')).toBeUndefined();
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

  it('links out through a provider chip, the way the movie header does', async () => {
    stubFetch();
    await mountSite();

    const chip = links().find((a) => a.getAttribute('href') === SITE.provider_url);
    expect(chip, 'the provider chip').toBeTruthy();
    expect(chip!.textContent).toContain('TPDB');
    expect(chip!.getAttribute('target')).toBe('_blank');
    expect(chip!.getAttribute('rel')).toBe('noopener noreferrer');
    // The id itself is plain text now, as the TMDB id is on a movie's page.
    expect(host!.textContent).toContain(SITE.stash_id);
    expect(chip!.textContent).not.toContain(SITE.stash_id);
  });

  it('renders the provider id as plain text when there is no page for it', async () => {
    stubFetch({ ...SITE, provider_url: '' });
    await mountSite();

    expect(host!.textContent).toContain(SITE.stash_id);
    expect(hrefs().some((href) => href.includes('theporndb.net/sites'))).toBe(false);
  });

  it('links a scene title to its provider page, never to the site itself', async () => {
    stubFetch();
    await mountSite();

    const scene = links().find(
      (a) => a.getAttribute('href') === 'https://theporndb.net/scenes/scene-3',
    );
    expect(scene, 'the scene title as a link').toBeTruthy();
    expect(scene!.textContent).toContain('Deep Impact');
    expect(host!.querySelector('#y2022n3')).not.toBeNull();
    expect(host!.querySelector('#y2022n5')).not.toBeNull();
    expect(scene!.getAttribute('rel')).toBe('noopener noreferrer');
    // The scene's own site url is metadata, not a destination.
    expect(hrefs().some((href) => href.includes('brazzers.com'))).toBe(false);
    // A scene the provider has no page for stays plain text.
    expect(host!.textContent).toContain('Shallow Impact');
    expect(links().filter((a) => a.getAttribute('href')?.includes('/scenes/'))).toHaveLength(2);
  });

  it('queues a downloaded scene for conversion', async () => {
    stubFetch();
    await mountSite();

    const row = [...host!.querySelectorAll('tr')].find((tr) =>
      tr.textContent?.includes('Crowd Scene'),
    );
    const button = [...row!.querySelectorAll('button')].find((candidate) =>
      candidate.textContent?.includes('Convert for TV'),
    );
    expect(button?.title).toContain('HEVC video');

    button!.click();
    await vi.waitFor(() => {
      if (!calls.some((call) => call.url === '/api/v1/convert')) {
        throw new Error('conversion was not queued');
      }
    });
    const post = calls.find((call) => call.url === '/api/v1/convert');
    expect(post).toMatchObject({
      method: 'POST',
      body: { media_file_id: 99 },
    });
    await vi.waitFor(() => {
      if (!row!.textContent?.includes('In the convert queue')) {
        throw new Error('conversion action did not enter its queued state');
      }
    });
  });

  it('hides conversion when ffmpeg is unavailable', async () => {
    stubFetch();
    await mountSite('admin', false);

    expect(buttonLabelled('Convert for TV')).toBeUndefined();
  });
});

describe('AdultSite for a granted member', () => {
  it('offers no action a member would be refused', async () => {
    stubFetch();
    await mountSite('member');

    expect(buttonLabelled('Search monitored')).toBeUndefined();
    expect(buttonLabelled('Convert for TV')).toBeUndefined();
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
  function rowText(title: string): string {
    return (
      [...host!.querySelectorAll('tr')].find((row) => row.textContent?.includes(title))
        ?.textContent ?? ''
    );
  }

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
      'Actions',
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

  it('keeps full site metadata and scene text on truncated values', async () => {
    stubFetch();
    await mountSite();

    const titles = [...host!.querySelectorAll<HTMLElement>('[title]')].map(
      (element) => element.title,
    );
    expect(titles).toContain(SITE.path);
    expect(titles).toContain(SITE.stash_id);
    expect(titles).toContain('#003 Deep Impact');
  });

  it('keeps the performers out of the title cell', async () => {
    stubFetch();
    await mountSite();

    const link = [...host!.querySelectorAll('a')].find(
      (a) => a.getAttribute('href') === 'https://theporndb.net/scenes/scene-3',
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

  it('refreshes scene statuses when grabs and imports change the library', async () => {
    let served = SITE;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        let body: unknown;
        if (url.endsWith('/adult/sites/7')) body = served;
        else if (url.includes('/system/status')) body = STATUS;
        else if (url.includes('/auth/me')) body = user('admin');
        else if (url.includes('/libraries')) body = { libraries: [] };
        else throw new Error(`unexpected fetch: ${url}`);
        return new Response(JSON.stringify(body), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }),
    );
    await mountSite();

    expect(rowText('Deep Impact')).toContain('Missing');
    expect(rowText('Shallow Impact')).toContain('Missing');

    served = {
      ...SITE,
      years: SITE.years.map((year) => ({
        ...year,
        scenes: year.scenes.map((scene) =>
          scene.id === 11 ? { ...scene, downloading: true } : scene,
        ),
      })),
    };
    applyInvalidation('library');
    await vi.waitFor(() => {
      if (!rowText('Deep Impact').includes('Downloading')) throw new Error('not downloading');
    });
    expect(rowText('Shallow Impact')).toContain('Missing');

    served = {
      ...served,
      scene_file_count: 2,
      years: served.years.map((year) => ({
        ...year,
        scenes: year.scenes.map((scene) =>
          scene.id === 11
            ? { ...scene, downloading: false, file: SITE.years[0]!.scenes[1]!.file }
            : scene,
        ),
      })),
    };
    applyInvalidation('library');
    await vi.waitFor(() => {
      if (!rowText('Deep Impact').includes('Downloaded')) throw new Error('not downloaded');
    });
    expect(rowText('Shallow Impact')).toContain('Missing');
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

  function editButton(): HTMLButtonElement | undefined {
    return [...host!.querySelectorAll<HTMLButtonElement>('button')].find(
      (candidate) => candidate.textContent?.trim() === 'Edit',
    );
  }

  async function openEditor(): Promise<HTMLElement> {
    editButton()?.click();
    flushSync();
    await vi.waitFor(() => {
      if (!host!.querySelector('[role="dialog"]')?.textContent?.includes('Playback target')) {
        throw new Error('edit dialog not ready');
      }
    });
    return host!.querySelector<HTMLElement>('[role="dialog"]')!;
  }

  function saveChanges(dialog: HTMLElement): HTMLButtonElement {
    return [...dialog.querySelectorAll<HTMLButtonElement>('button')].find(
      (candidate) => candidate.textContent?.trim() === 'Save changes',
    )!;
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

  it('keeps Edit and overflow beside the title', async () => {
    stubFetch();
    await mountSite();

    const titleControls = host!.querySelector('h2')?.parentElement;
    expect(titleControls?.contains(editButton()!)).toBe(true);
    expect(titleControls?.contains(menuTrigger()!)).toBe(true);
  });

  async function flip(label: string) {
    const control = toggle(label);
    expect(control, `a control for ${label}`).toBeTruthy();
    control!.click();
    await vi.waitFor(() => {
      if (!calls.some((c) => c.method === 'PATCH')) throw new Error('no write yet');
    });
  }

  it('turns the whole site off through the series route', async () => {
    stubFetch();
    await mountSite();

    const dialog = await openEditor();
    expect(dialog.textContent).toContain('Adult Archive');
    expect(dialog.textContent).toContain('Capable');
    dialog.querySelector<HTMLButtonElement>('button[role="switch"]')!.click();
    flushSync();
    saveChanges(dialog).click();
    await vi.waitFor(() => {
      if (!calls.some((call) => call.method === 'PATCH')) throw new Error('no write yet');
    });
    const patch = calls.find((c) => c.method === 'PATCH');
    expect(patch?.url).toBe('/api/v1/library/series/7');
    expect(patch?.body).toEqual({ monitored: false, quality_profile_id: 0 });
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

    const dialog = await openEditor();
    dialog.querySelector<HTMLButtonElement>('button[role="switch"]')!.click();
    flushSync();
    saveChanges(dialog).click();
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

    expect(editButton()).toBeUndefined();
    expect(menuTrigger()).toBeUndefined();
    expect(host!.querySelector('[role="menu"]')).toBeNull();
    expect(toggle('Monitor 2022')).toBeUndefined();
    expect(toggle('Monitor #003')).toBeUndefined();
    // But the state itself is still readable: an unmonitored year says so.
    expect(host!.textContent).toContain('2022');
  });
});

/**
 * The catalogue walk, watched.
 *
 * A site is added and its scenes arrive a release year at a time from a
 * background job, so this page is the one place in the app that has to show
 * work happening somewhere else. The properties below are what "reactive" has
 * to mean for it to be worth anything: the page re-reads itself while the walk
 * runs, the years appear without a reload, it stops when the walk does, and a
 * background read never takes the page away from the reader.
 */
describe('AdultSite cataloguing', () => {
  /** What the next GET will answer with; tests move it as the walk progresses. */
  let served: Record<string, unknown> = SITE;
  let failNext = false;

  const EMPTY_CATALOGUING = { ...SITE, cataloguing: true, scene_count: 0, years: [] };
  const PARTLY_CATALOGUED = { ...SITE, cataloguing: true, scene_count: 3 };
  const DONE = { ...SITE, cataloguing: false };

  function stubPolling(initial: Record<string, unknown>) {
    served = initial;
    failNext = false;
    calls = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const method = init?.method ?? 'GET';
        calls.push({ url: String(input), method, body: null });
        if (failNext) {
          failNext = false;
          return new Response(JSON.stringify({ error: 'database is locked' }), {
            status: 500,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        return new Response(JSON.stringify(served), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }),
    );
  }

  async function mountPolling(): Promise<HTMLElement> {
    session.user = user('admin');
    host = document.createElement('div');
    document.body.appendChild(host);
    app = mount(AdultSite, { target: host, props: { id: 7 } }) as Record<string, unknown>;
    flushSync();
    // Let onMount's first load resolve under fake timers.
    await vi.advanceTimersByTimeAsync(0);
    flushSync();
    return host;
  }

  async function tick(times = 1) {
    for (let i = 0; i < times; i++) {
      await vi.advanceTimersByTimeAsync(CATALOGUING_POLL_MS);
      flushSync();
    }
  }

  const reads = () => calls.filter((c) => c.method === 'GET').length;

  it('shows the cataloguing state instead of an empty site, and no reload advice', async () => {
    vi.useFakeTimers();
    stubPolling(EMPTY_CATALOGUING);
    await mountPolling();

    expect(host!.textContent).toContain('Cataloguing scenes');
    // The old copy told the reader to do the page's job for it.
    expect(host!.textContent).not.toContain('reload');
    expect(host!.textContent).not.toContain('No scenes yet');
  });

  it('polls while cataloguing and shows years as they land, without a remount', async () => {
    vi.useFakeTimers();
    stubPolling(EMPTY_CATALOGUING);
    await mountPolling();
    expect(host!.textContent).not.toContain('Deep Impact');

    // The walk publishes 2022; the next poll is what makes it visible.
    served = PARTLY_CATALOGUED;
    await tick();

    expect(host!.textContent).toContain('Deep Impact');
    expect(host!.textContent).toContain('2022');
  });

  it('swaps the empty state for a slim banner once scenes are on screen', async () => {
    vi.useFakeTimers();
    stubPolling(PARTLY_CATALOGUED);
    await mountPolling();

    // Still working, but there is something to look at — so the page keeps the
    // scenes and says so in one line, with the live count.
    expect(host!.textContent).toContain('Deep Impact');
    expect(host!.textContent).toContain('Cataloguing 3 scenes.');
    expect(host!.textContent).not.toContain('there is nothing to do but watch');
  });

  it('stops polling once the walk finishes, after one last read', async () => {
    vi.useFakeTimers();
    stubPolling(PARTLY_CATALOGUED);
    await mountPolling();
    const afterMount = reads();

    // The walk ends: this poll observes cataloguing going false.
    served = DONE;
    await tick();
    const afterEnd = reads();
    expect(afterEnd).toBeGreaterThan(afterMount);

    // One final read settles whatever landed with the last year, and then the
    // page goes quiet however long it is left open.
    await tick();
    const settled = reads();
    await tick(5);
    expect(reads()).toBe(settled);
    expect(settled).toBe(afterEnd + 1);
    expect(host!.textContent).not.toContain('Cataloguing scenes');
  });

  it('never polls a site that was not being catalogued when it loaded', async () => {
    vi.useFakeTimers();
    stubPolling(DONE);
    await mountPolling();
    const afterMount = reads();

    await tick(5);
    expect(reads()).toBe(afterMount);
  });

  it('keeps the rendered page through a background poll, skeleton and all', async () => {
    vi.useFakeTimers();
    stubPolling(PARTLY_CATALOGUED);
    await mountPolling();

    // A poll must not flash the loading skeleton over a page that is already
    // showing its scenes.
    const skeletons = () => host!.querySelectorAll('.animate-pulse').length;
    expect(skeletons()).toBe(0);
    await tick();
    expect(skeletons()).toBe(0);
    expect(host!.textContent).toContain('Deep Impact');
  });

  it('does not throw the reader out of a rendered page when a poll fails', async () => {
    vi.useFakeTimers();
    stubPolling(PARTLY_CATALOGUED);
    await mountPolling();
    expect(host!.textContent).toContain('Deep Impact');

    failNext = true;
    await tick();

    // The error branch wins over everything in this template, so a background
    // failure that set it would replace a perfectly good page with a banner.
    expect(host!.textContent).not.toContain('Something went wrong');
    expect(host!.textContent).toContain('Deep Impact');

    // And the next tick recovers on its own.
    await tick();
    expect(host!.textContent).toContain('Deep Impact');
  });
});
