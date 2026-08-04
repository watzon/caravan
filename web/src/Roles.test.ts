/**
 * What a member's Caravan looks like (SPEC §11), end to end through the real
 * shell: the navigation they get, the screens they are kept off, and the one
 * verb Discover offers them.
 *
 * The server is the enforcer — every route below answers 403 for a member
 * whatever this file proves — so what is being tested is that the SPA does not
 * walk them into a wall. It lives in its own file because `session` is a module
 * singleton and a test that left it a member would rewrite every test after it.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import App from './App.svelte';
import type { DiscoverHome, SessionUser, SystemStatus } from './lib/api/types';
import { navigate, router } from './lib/router.svelte';
import { discover } from './lib/state/discover.svelte';
import { session } from './lib/state/session.svelte';
import { system } from './lib/state/system.svelte';
import { auth } from './lib/state/auth.svelte';
import { clearToasts } from './lib/state/toast.svelte';

const MEMBER: SessionUser = { username: 'ada', role: 'member', open: false, adult: false };
const ADMIN: SessionUser = { username: 'root', role: 'admin', open: false, adult: false };
/**
 * The two identities the adult module IS visible to. `adult: true` is the
 * server's already-ANDed answer — the module is switched on AND this account
 * reaches it — so these two differ from the pair above in exactly one field,
 * which is the point: nothing about the module is inferred from the role.
 */
const GRANTED_MEMBER: SessionUser = { ...MEMBER, adult: true };
const GRANTED_ADMIN: SessionUser = { ...ADMIN, adult: true };

const STATUS: SystemStatus = {
  version: '0.1.0',
  mode: 'server',
  storage_root: '/data',
  schema_version: 11,
  scanning: false,
  counts: { movies: 0, series: 0, media_files: 0, unmatched: 0 },
  disk_free_bytes: 1024,
  disk_total_bytes: 2048,
  engine_health: 'ok',
  ffmpeg_available: true,
  password_set: true,
  listening_publicly: false,
};

/** The billboard title. `inLibrary` decides whether Caravan already has it. */
function discoverHome(): DiscoverHome {
  return {
    trending: [
      {
        media_type: 'series',
        tmdb_id: 95396,
        title: 'Severance',
        year: 2022,
        overview: 'Work-life balance, surgically.',
        poster_path: '/p.jpg',
        poster_url: '',
        backdrop_url: '',
        vote_average: 8.4,
        date: '2022-02-18',
        in_library: inLibrary,
        library_id: inLibrary ? 7 : 0,
        requested: false,
      },
    ],
    popular_movies: [],
    popular_series: [],
    networks: [],
    studios: [],
  };
}

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
/** Who /auth/me answers as; a test sets it before mounting. */
let me: SessionUser = MEMBER;
/** Whether the billboard title is already in the library. */
let inLibrary = false;
let requested: string[];

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

async function settle() {
  for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

/** Mount the shell at `path`, as whoever `me` currently is. */
async function open(path: string) {
  window.history.replaceState({}, '', path);
  navigate(path, { replace: true });
  app = mount(App, { target: host }) as Record<string, unknown>;
  await settle();
}

beforeEach(() => {
  me = MEMBER;
  inLibrary = false;
  requested = [];
  discover.reset();
  window.scrollTo = () => {};
  window.sessionStorage.clear();
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      requested.push(url);
      if (url.endsWith('/auth/me')) return jsonResponse(me);
      if (url.endsWith('/system/status')) return jsonResponse(STATUS);
      if (url.endsWith('/discover')) return jsonResponse(discoverHome());
      if (url.includes('/requests')) return jsonResponse({ requests: [] });
      if (url.endsWith('/downloads')) return jsonResponse({ downloads: [] });
      if (url.endsWith('/library/movies')) return jsonResponse({ movies: [] });
      // Answered so that an adult screen which DOES render has something to
      // render. Whether these are ever called is itself asserted below.
      if (url.includes('/adult/sites')) return jsonResponse({ sites: [] });
      // The scene picker reads the site through the series routes, because a
      // site IS a series row. Answered for the same reason the line above is.
      if (url.includes('/library/series/7/releases')) return jsonResponse({ releases: [] });
      if (url.includes('/library/series/7')) {
        return jsonResponse({ id: 7, title: 'Brazzers', seasons: [] });
      }
      if (url.includes('/adult/discover')) {
        return jsonResponse({ page: 1, per_page: 20, total: 0, scenes: [] });
      }
      throw new Error(`unexpected fetch: ${url}`);
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
  session.forget();
  auth.required = false;
  system.status = null;
  system.loading = true;
  discover.reset();
  clearToasts();
});

describe('a member', () => {
  it('gets the Explore group and nothing else', async () => {
    await open('/discover');

    expect(host.querySelector('aside')).not.toBeNull();
    expect(host.textContent).toContain('Explore');
    expect(host.querySelector('a[href="/discover"]')).not.toBeNull();
    expect(host.querySelector('a[href="/requests"]')).not.toBeNull();

    for (const group of ['Library', 'Activity', 'Manage']) {
      expect(host.textContent, group).not.toContain(group);
    }
    for (const href of ['/movies', '/series', '/wanted', '/queue', '/settings', '/scan-review']) {
      expect(host.querySelector(`a[href="${href}"]`), href).toBeNull();
    }
    // No "Add movie or series" in the top bar either: there is no library to
    // add to.
    expect(host.textContent).not.toContain('Add movie or series');
  });

  it('never asks for the admin-only system status', async () => {
    await open('/discover');

    expect(requested.some((url) => url.endsWith('/system/status'))).toBe(false);
    // …and therefore never renders the failure of not being allowed to.
    expect(host.textContent).not.toContain('Caravan server unreachable');
  });

  it('is sent to Discover when it lands on an admin screen', async () => {
    await open('/settings');

    expect(router.path).toBe('/discover');
    expect(host.textContent).toContain('Explore');
    // The settings rail never rendered on the way past.
    expect(host.textContent).not.toContain('Quality profiles');
  });

  it('is never sent to first run, even when the server needs setup', async () => {
    // Stale admin knowledge in the singleton: the tab loaded the status while
    // it was an admin's, then the session became a member's. Before the
    // first-run gate was made admin-only, this state made the two route
    // effects chase each other — first-run is not a member route, so the
    // member guard undid the first-run redirect, forever. `loading` must be
    // false too, or the gate never reads needsSetup and the test proves
    // nothing.
    system.status = { ...STATUS, storage_root: '' };
    system.loading = false;
    await open('/first-run');

    expect(router.path).toBe('/discover');
    expect(host.textContent).not.toContain('Choose a storage root');
  });

  it('is offered Request on the Discover billboard, never Add', async () => {
    await open('/discover');

    expect(host.textContent).toContain('Request series');
    expect(host.textContent).not.toContain('Add series');
  });

  /**
   * /series/:id is an admin screen, so a link to it from the billboard would
   * bounce the member straight back here — from the hero it would read as a
   * button that does nothing. The fact is still worth stating, so it is stated
   * rather than linked.
   */
  it('is told a billboard title is in the library without being linked into it', async () => {
    inLibrary = true;
    await open('/discover');

    expect(host.textContent).toContain('In library');
    expect(host.querySelector('a[href="/series/7"]')).toBeNull();
    expect(router.path).toBe('/discover');
  });

  it('opens the modal in request mode, so no quality profile is on offer', async () => {
    await open('/discover');

    const button = [...host.querySelectorAll('button')].find((b) =>
      b.textContent?.includes('Request series'),
    );
    expect(button).toBeDefined();
    button!.click();
    await settle();

    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog).not.toBeNull();
    expect(dialog?.textContent).toContain('Request');
    expect(dialog?.textContent).not.toContain('Add to library');
    expect(dialog?.querySelector('#add-profile')).toBeNull();
    expect(dialog?.querySelector('#add-root')).toBeNull();
  });
});

/**
 * The adult module's conditional visibility (PLAN phase 9 track 5), across the
 * disabled/enabled × admin/granted/ungranted matrix, through the real shell.
 *
 * `SessionUser.adult` is the server's already-ANDed answer, so "module off" and
 * "account not granted" are the same input here — which is exactly the design:
 * off and not-granted must be indistinguishable from the browser's side.
 *
 * The bar is ZERO trace, not "no link": no nav item, no route, and nothing on
 * the wire. A request to /adult/sites that gets redirected a tick later has
 * still been sent, and would still be in a log.
 */
describe('the adult module — not visible', () => {
  /** Every identity the module is NOT visible to, including an admin. */
  const ungranted: [string, SessionUser][] = [
    ['an ungranted member', MEMBER],
    // The case a role check would get wrong: an admin on a server with the
    // module switched off. Their role opens every other screen in the app.
    ['an admin with the module off', ADMIN],
  ];

  for (const [who, identity] of ungranted) {
    it(`shows ${who} no Adult nav item and no adult traffic`, async () => {
      me = identity;
      await open('/discover');

      expect(host.querySelector('a[href="/adult"]')).toBeNull();
      expect(host.querySelector('a[href="/adult/scenes"]')).toBeNull();
      expect(host.textContent).not.toContain('Adult');
      expect(requested.some((url) => url.includes('/adult'))).toBe(false);
    });

    it(`sends ${who} away from /adult without ever calling it`, async () => {
      me = identity;
      await open('/adult');

      // An admin lands on their own shelf, a member on theirs; neither lands
      // on a screen whose every call would answer 404.
      expect(router.path).toBe(identity.role === 'admin' ? '/movies' : '/discover');
      // The render is gated as well as the redirect, so the site grid never
      // mounted and never put a request on the wire on its way past.
      expect(requested.some((url) => url.includes('/adult'))).toBe(false);
      expect(host.textContent).not.toContain('Adult');
    });

    it(`sends ${who} away from the scene search too`, async () => {
      me = identity;
      await open('/adult/scenes');

      expect(router.path).toBe(identity.role === 'admin' ? '/movies' : '/discover');
      expect(requested.some((url) => url.includes('/adult'))).toBe(false);
    });

    it(`sends ${who} away from the scene picker, without searching anything`, async () => {
      me = identity;
      await open('/adult/sites/7/search/2022/3');

      expect(router.path).toBe(identity.role === 'admin' ? '/movies' : '/discover');
      // The picker reads a site through the series routes; an identity the
      // module is invisible to must not put either kind of request on the wire.
      expect(requested.some((url) => url.includes('/adult'))).toBe(false);
      expect(requested.some((url) => url.includes('/library/series/7'))).toBe(false);
    });
  }
});

describe('the adult module — visible', () => {
  const granted: [string, SessionUser][] = [
    ['a granted member', GRANTED_MEMBER],
    ['a granted admin', GRANTED_ADMIN],
  ];

  for (const [who, identity] of granted) {
    it(`gives ${who} the Adult nav item`, async () => {
      me = identity;
      await open('/discover');

      expect(host.querySelector('a[href="/adult"]')).not.toBeNull();
      expect(host.textContent).toContain('Adult');
      // The shelf lives in the Library group, which a member otherwise has
      // none of — a granted member's Library group holds this and nothing else.
      expect(host.textContent).toContain('Library');
    });

    it(`lets ${who} open the site grid`, async () => {
      me = identity;
      await open('/adult');

      expect(router.path).toBe('/adult');
      expect(requested.some((url) => url.includes('/adult/sites'))).toBe(true);
    });

    it(`lets ${who} open the scene search`, async () => {
      me = identity;
      await open('/adult/scenes');

      expect(router.path).toBe('/adult/scenes');
      expect(requested.some((url) => url.includes('/adult/discover'))).toBe(true);
    });

    it(`opens the scene picker for ${who} only if they may grab`, async () => {
      me = identity;
      await open('/adult/sites/7/search/2022/3');

      if (identity.role === 'admin') {
        expect(router.path).toBe('/adult/sites/7/search/2022/3');
        expect(requested.some((url) => url.includes('/library/series/7/releases'))).toBe(true);
      } else {
        // The grant opens the adult SCREENS; grabbing a release is still an
        // admin write, and the server keeps those routes admin-only. Both gates
        // have to say yes, and here the role one says no.
        expect(router.path).toBe('/discover');
        expect(requested.some((url) => url.includes('/library/series/7'))).toBe(false);
      }
    });
  }

  /**
   * The grant is about the module, not about the rest of the app: a granted
   * member is still a member everywhere else.
   */
  it('does not widen a granted member past the module', async () => {
    me = GRANTED_MEMBER;
    await open('/settings');

    expect(router.path).toBe('/discover');
    for (const href of ['/movies', '/series', '/wanted', '/queue', '/settings']) {
      expect(host.querySelector(`a[href="${href}"]`), href).toBeNull();
    }
  });
});

describe('an admin', () => {
  it('keeps every nav group and the direct add', async () => {
    me = ADMIN;
    await open('/discover');

    for (const group of ['Explore', 'Library', 'Activity', 'Manage']) {
      expect(host.textContent, group).toContain(group);
    }
    expect(host.querySelector('a[href="/settings"]')).not.toBeNull();
    expect(host.textContent).toContain('Add series');
    expect(requested.some((url) => url.endsWith('/system/status'))).toBe(true);
  });

  /** The library screens are theirs, so the billboard still opens them. */
  it('keeps the billboard link into the library', async () => {
    me = ADMIN;
    inLibrary = true;
    await open('/discover');

    expect(host.querySelector('a[href="/series/7"]')).not.toBeNull();
  });
});
