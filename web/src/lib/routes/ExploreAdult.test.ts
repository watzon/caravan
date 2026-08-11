/**
 * Explore's adult scope, mounted for real — the screen that replaced the Adult
 * section's Scenes tab.
 *
 * Two things are being proved here beyond the filter model: that the retired
 * tab's flow survived the move intact (three card states, one Request body), and
 * that a filter the CONFIGURED endpoint cannot express is reported as such
 * rather than as a broken screen. That second one is unique to this scope: the
 * stash-box dialects disagree about what they can answer, and the server refuses
 * rather than serving an unfiltered page.
 *
 * The visibility gate is NOT tested here — it belongs to the shell, and
 * Roles.test.ts exercises it through App.svelte across the whole identity
 * matrix. By the time this component mounts, `session.adult` is already true.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import ExploreAdult from './ExploreAdult.svelte';
import type { SceneFilterSupport, SceneMeta, SessionUser } from '../api/types';
import { EVERY_SCENE_FILTER } from '../adult';
import { DEBOUNCE_MS } from '../typeahead';
import { navigate, router } from '../router.svelte';
import { session } from '../state/session.svelte';
import { clearToasts } from '../state/toast.svelte';

function scene(id: string, extra: Partial<SceneMeta> = {}): SceneMeta {
  return {
    media_type: 'scene',
    stash_id: id,
    site_stash_id: 'site-1',
    site_name: 'Vixen',
    title: `Scene ${id}`,
    overview: '',
    date: '2026-07-12',
    duration: 2472,
    performers: ['Sienna Vale', 'Mara Solis'],
    url: '',
    image_url: '/img.jpg',
    in_library: false,
    library_id: 0,
    requested: false,
    ...extra,
  };
}

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let requested: string[];
/** Bodies of every POST, so the Request call can be read back. */
let posted: { url: string; body: unknown }[];
let served: SceneMeta[];
/** When set, /adult/discover answers this instead of a page. */
let failWith: { status: number; error: string } | null;
/**
 * The paging the provider reports, overridable so "there is another page" can
 * be staged without inventing twenty-five scenes.
 */
let paging: { per_page: number; total: number } | null;

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function lastSceneQuery(): URLSearchParams {
  const url = [...requested].reverse().find((u) => u.includes('/adult/discover'));
  return new URL(String(url), 'http://x').searchParams;
}

async function settle() {
  for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

async function open(url: string) {
  window.history.replaceState({}, '', url);
  navigate(url, { replace: true });
  app = mount(ExploreAdult, { target: host }) as Record<string, unknown>;
  await settle();
}

function buttonWithText(text: string): HTMLButtonElement | undefined {
  return [...host.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.textContent?.trim() === text,
  );
}

/** Every filter pill's label — which controls the rail is offering at all. */
function pillLabels(): string[] {
  return [...host.querySelectorAll<HTMLButtonElement>('button[aria-expanded]')].map((b) =>
    (b.textContent ?? '').trim(),
  );
}

/** The sort dropdown's options, by the name a reader sees. */
async function sortLabels(): Promise<string[]> {
  const trigger = [...host.querySelectorAll<HTMLButtonElement>('button[aria-expanded]')].find(
    (b) => (b.textContent ?? '').trim().startsWith('Sort'),
  );
  expect(trigger, 'the sort dropdown').toBeTruthy();
  trigger!.click();
  await settle();
  const panel = host.querySelector<HTMLElement>('[role="dialog"][aria-label^="Sort"]');
  const labels = [...(panel?.querySelectorAll<HTMLButtonElement>('button') ?? [])].map((b) =>
    (b.textContent ?? '').trim(),
  );
  // A pick (or a look) leaves the popover open; dismiss it as a reader does.
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
  await settle();
  return labels;
}

/** A pill renders its body lazily, so a control inside one needs it opened. */
function openPill(label: string) {
  const pill = [...host.querySelectorAll<HTMLButtonElement>('button[aria-expanded]')].find(
    (b) => (b.textContent ?? '').trim() === label,
  );
  pill?.click();
  flushSync();
}

/** A granted member on a server whose endpoint serves `filters`. */
function grantedUser(filters: SceneFilterSupport): SessionUser {
  return { username: 'sam', role: 'member', open: false, adult: true, scene_filters: filters };
}

const NO_SCENE_FILTER: SceneFilterSupport = {
  year: false,
  duration: false,
  site_scope: false,
  date_op: false,
  sort_duration: false,
  sort_relevance: false,
  any_of: false,
};

beforeEach(() => {
  requested = [];
  posted = [];
  served = [scene('a')];
  failWith = null;
  paging = null;
  window.scrollTo = () => {};
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      requested.push(url);
      if (init?.method === 'POST') {
        posted.push({ url, body: JSON.parse(String(init.body)) });
        return jsonResponse({ id: 1, status: 'pending' }, 201);
      }
      if (url.includes('/adult/discover')) {
        if (failWith) return jsonResponse({ error: failWith.error }, failWith.status);
        return jsonResponse({
          page: 1,
          per_page: paging?.per_page ?? 25,
          total: paging?.total ?? served.length,
          scenes: served,
        });
      }
      if (url.includes('/adult/performers')) {
        return jsonResponse({ performers: [{ id: '84060', name: 'Mia Malkova', image_url: '' }] });
      }
      if (url.includes('/adult/tags')) return jsonResponse({ tags: [] });
      if (url.includes('/adult/search')) return jsonResponse({ sites: [] });
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
  clearToasts();
  session.forget();
  navigate('/', { replace: true });
});

describe('a filtered scene scope', () => {
  it('names the scene search, filters, toggle, and sort control', async () => {
    session.user = grantedUser(EVERY_SCENE_FILTER);
    await open('/discover/adult');

    expect(host.querySelector('input[type="search"]')?.getAttribute('aria-label')).toBe(
      'Search the metadata provider for scenes',
    );
    expect(buttonWithText('Search')).toBeDefined();
    expect(pillLabels()).toEqual(['Site', 'Performers', 'Tags', 'Year', 'Duration', 'Sort: Newest']);
    expect(host.querySelector('[role="switch"]')?.textContent?.trim()).toBe('Hide in library');
  });

  it('turns a shared URL into one request carrying every filter', async () => {
    await open(
      '/discover/adult?q=poolside&site=84060:Vixen&scope=network' +
        '&performers=1:Sienna+Vale&performers=2:Mara+Solis&performers_all=true' +
        '&tags=70:Outdoor&tags=71:Threesome&tags_all=true' +
        '&year=2026&duration=40&sort=duration&order=desc',
    );

    const query = lastSceneQuery();
    expect(query.get('q')).toBe('poolside');
    // The site goes over as its id alone; the name is the chip's, not the
    // provider's, for a value the provider looks up by id.
    expect(query.get('site')).toBe('84060');
    expect(query.get('scope')).toBe('network');
    // Repeated parameters, and each carrying its name: a performer's name may
    // contain a comma, and the provider filters on `performers[1]=Sienna Vale`.
    expect(query.getAll('performers')).toEqual(['1:Sienna Vale', '2:Mara Solis']);
    expect(query.get('performers_all')).toBe('true');
    expect(query.getAll('tags')).toEqual(['70:Outdoor', '71:Threesome']);
    expect(query.get('tags_all')).toBe('true');
    expect(query.get('year')).toBe('2026');
    // Minutes on the rail, seconds on the wire.
    expect(query.get('duration')).toBe('2400');
    expect(query.get('sort')).toBe('duration');
    expect(query.get('order')).toBe('desc');
  });

  it('restores the applied chips from the URL', async () => {
    await open('/discover/adult?site=84060:Vixen&scope=network&tags=70:Outdoor&year=2026');

    expect(host.textContent).toContain('Site: Vixen (Whole network)');
    expect(host.textContent).toContain('Tag: Outdoor');
    expect(host.textContent).toContain('Year: 2026');
  });

  /**
   * The any/all mode chip appears only once there are two things to combine:
   * over one id the two readings are the same question, and a mode nothing on
   * screen explains is worse than no mode at all.
   */
  it('offers and announces the any/all modes only with multiple values', async () => {
    session.user = grantedUser(EVERY_SCENE_FILTER);
    await open('/discover/adult?tags=70:Outdoor');
    expect(host.textContent).not.toContain('tags: any');

    unmount(app!);
    app = undefined;
    await open(
      '/discover/adult?performers=1:Sienna+Vale&performers=2:Mara+Solis' +
        '&tags=70:Outdoor&tags=71:Threesome',
    );

    const performerAny = buttonWithText('performers: any')!;
    const tagsAny = buttonWithText('tags: any')!;
    expect(performerAny.getAttribute('aria-label')).toBe('Match performers: any');
    expect(performerAny.getAttribute('aria-pressed')).toBe('false');
    expect(tagsAny.getAttribute('aria-label')).toBe('Match tags: any');
    expect(tagsAny.getAttribute('aria-pressed')).toBe('false');

    tagsAny.click();
    await settle();
    const tagsAll = buttonWithText('tags: all')!;
    expect(tagsAll.getAttribute('aria-label')).toBe('Match tags: all');
    expect(tagsAll.getAttribute('aria-pressed')).toBe('true');
    expect(lastSceneQuery().get('tags_all')).toBe('true');

    buttonWithText('performers: any')!.click();
    await settle();
    const performersAll = buttonWithText('performers: all')!;
    expect(performersAll.getAttribute('aria-label')).toBe('Match performers: all');
    expect(performersAll.getAttribute('aria-pressed')).toBe('true');
    expect(lastSceneQuery().get('performers_all')).toBe('true');
  });

  it('drops the site scope with the site when its chip is removed', async () => {
    await open('/discover/adult?site=84060:Vixen&scope=network');

    host
      .querySelector<HTMLButtonElement>('button[aria-label="Remove filter Site: Vixen (Whole network)"]')
      ?.click();
    await settle();

    expect(router.search).toBe('');
    // `scope` with no `site` to widen is a 400; it must never be sent alone.
    expect(lastSceneQuery().get('scope')).toBeNull();
    expect(lastSceneQuery().get('site')).toBeNull();
  });

  it('hides owned scenes in the browser without re-asking the provider', async () => {
    served = [scene('a', { title: 'Owned', in_library: true }), scene('b', { title: 'Free' })];
    await open('/discover/adult?hide=1');

    expect(lastSceneQuery().get('hide')).toBeNull();
    expect(host.textContent).toContain('Free');
    expect(host.textContent).not.toContain('Owned');
  });
});

describe('a scene card', () => {
  it('reads its state as owned, requested, or askable — in that order', async () => {
    served = [
      scene('a', { title: 'Owned one', in_library: true, requested: true }),
      scene('b', { title: 'Asked for', requested: true }),
      scene('c', { title: 'Free one' }),
    ];
    await open('/discover/adult');

    // Owned beats requested: once the library holds it, the request is moot.
    expect(host.textContent).toContain('IN LIBRARY');
    expect(host.textContent).toContain('REQUESTED');
    expect(host.querySelectorAll('button').length).toBeGreaterThan(0);
    expect(buttonWithText('Request')).toBeDefined();
    // One Request button, for the one card that has neither state.
    expect(
      [...host.querySelectorAll('button')].filter((b) => b.textContent?.trim() === 'Request'),
    ).toHaveLength(1);
  });

  it('badges the run time as a run time', async () => {
    await open('/discover/adult');
    expect(host.textContent).toContain('41:12');
  });

  /**
   * The retired Scenes tab's request body, unchanged: a scene is named by its
   * stash-box id and nothing else, and the server refuses one that also carries
   * a tmdb id.
   */
  it('asks for a scene by stash id and no tmdb id', async () => {
    await open('/discover/adult');

    buttonWithText('Request')?.click();
    await settle();

    expect(posted).toHaveLength(1);
    expect(posted[0]?.body).toEqual({
      media_type: 'scene',
      tmdb_id: 0,
      stash_id: 'a',
      title: 'Scene a',
      year: 2026,
      poster_path: '/img.jpg',
    });
  });

  /** The card is patched in place: one flag changed, and a refetch is a round trip. */
  it('turns the button into a badge without refetching', async () => {
    await open('/discover/adult');
    const before = requested.filter((u) => u.includes('/adult/discover')).length;

    buttonWithText('Request')?.click();
    await settle();

    expect(host.textContent).toContain('REQUESTED');
    expect(buttonWithText('Request')).toBeUndefined();
    expect(requested.filter((u) => u.includes('/adult/discover')).length).toBe(before);
  });
});

describe('an endpoint that cannot answer', () => {
  /**
   * The dialect seam. A generic stash-box endpoint cannot widen a site scope or
   * filter by year, and the server names the filter rather than serving an
   * unfiltered page. That is a 400 the reader has to be able to act on, so it
   * is not a Retry — it is a way out of the filter.
   */
  it('names the filter and offers a way out, not a retry', async () => {
    failWith = { status: 400, error: 'the metadata endpoint cannot filter scenes by year' };
    await open('/discover/adult?year=2026');

    expect(host.textContent).toContain('the metadata endpoint cannot filter scenes by year');
    expect(buttonWithText('Retry')).toBeUndefined();

    buttonWithText('Clear filters')?.click();
    await settle();

    expect(router.search).toBe('');
  });

  /** 503 is a missing credential: a setup problem with a destination. */
  it('sends a missing stash-box credential to settings', async () => {
    failWith = { status: 503, error: 'no metadata credential' };
    await open('/discover/adult');

    expect(host.textContent).toContain('No metadata source configured');
    // The endpoints are configured on Metadata now (PLAN Part 2 phase 8), and
    // the adult settings page it used to point at no longer exists.
    expect(host.querySelector('a[href="/settings/metadata"]')).not.toBeNull();
  });

  it('offers a retry for an unhappy provider', async () => {
    failWith = { status: 502, error: 'upstream said no' };
    await open('/discover/adult');

    expect(host.textContent).toContain('upstream said no');
    expect(buttonWithText('Retry')).toBeDefined();
  });
});

/**
 * A FilterPill renders its body lazily, so nothing that stays shut ever issues
 * the typeahead's request — which is how the Site pill shipped calling an
 * endpoint members were refused. Opening it is the whole point of this one.
 */
describe('the Site pill', () => {
  it('searches sites for a granted member', async () => {
    session.user = grantedUser(EVERY_SCENE_FILTER);
    await open('/discover/adult');
    openPill('Site');

    const box = host.querySelector<HTMLInputElement>('input[aria-label="Search sites"]');
    expect(box).not.toBeNull();
    (box as HTMLInputElement).value = 'vixen';
    box?.dispatchEvent(new Event('input', { bubbles: true }));
    // Past the typeahead's debounce, which is real time here.
    await new Promise((resolve) => setTimeout(resolve, DEBOUNCE_MS + 20));
    await settle();

    expect(requested.some((u) => u.includes('/adult/search'))).toBe(true);
    // The popover shows results, not a refusal.
    expect(host.textContent).not.toContain('admins only');
  });
});

describe('the rail on an endpoint that serves less', () => {
  /**
   * PLAN phase 12's first acceptance criterion has two clauses, and this is the
   * second: "nothing renders a control the provider cannot answer". A generic
   * stash-box endpoint refuses a release year, a runtime, a widened site scope
   * and two of the six orderings, so on one of those the rail draws none of
   * them — a 400 that blanks the grid is not a control, it is a trap.
   */
  it('draws every pill on an endpoint that serves everything', async () => {
    session.user = grantedUser(EVERY_SCENE_FILTER);
    await open('/discover/adult');

    expect(pillLabels()).toContain('Year');
    expect(pillLabels()).toContain('Duration');
    expect(await sortLabels()).toContain('Longest');
    expect(await sortLabels()).toContain('Relevance');
  });

  it('draws no pill for a filter the endpoint refuses', async () => {
    session.user = grantedUser(NO_SCENE_FILTER);
    await open('/discover/adult');

    expect(pillLabels()).not.toContain('Year');
    expect(pillLabels()).not.toContain('Duration');
    expect(await sortLabels()).not.toContain('Longest');
    expect(await sortLabels()).not.toContain('Relevance');
    // The filters every dialect serves are untouched.
    expect(pillLabels()).toContain('Site');
    expect(pillLabels()).toContain('Performers');
    expect(pillLabels()).toContain('Tags');
    expect(await sortLabels()).toContain('Newest');
  });

  /**
   * The widening ladder is inside the Site pill, so it is only reachable with a
   * site already picked — which is also the only state it means anything in.
   */
  it('hides the widening ladder on an endpoint with no widening operator', async () => {
    session.user = grantedUser(NO_SCENE_FILTER);
    await open('/discover/adult?site=84060:Vixen');
    openPill('Site');

    expect(host.textContent).not.toContain('Whole network');

    unmount(app as Record<string, unknown>);
    app = undefined;
    session.user = grantedUser(EVERY_SCENE_FILTER);
    await open('/discover/adult?site=84060:Vixen');
    openPill('Site');

    expect(host.textContent).toContain('Whole network');
  });

  /**
   * An "any" that the endpoint would refuse is not offered. The chip row still
   * says `all`, so what is being asked is never left to a guess.
   */
  it('hides the any/all switch on an endpoint that only knows all-of', async () => {
    session.user = grantedUser(NO_SCENE_FILTER);
    await open('/discover/adult?tags=70:Outdoor&tags=71:Threesome&tags_all=true');

    expect(buttonWithText('tags: all')).toBeUndefined();

    unmount(app as Record<string, unknown>);
    app = undefined;
    session.user = grantedUser(EVERY_SCENE_FILTER);
    await open('/discover/adult?tags=70:Outdoor&tags=71:Threesome&tags_all=true');

    expect(buttonWithText('tags: all')).toBeDefined();
  });

  /**
   * The one filter Clear cannot normally touch. `clearedSceneFilter` keeps the
   * sort deliberately, but a link carrying `sort=duration` opened against an
   * endpoint with no duration ordering would then 400 again on every Clear —
   * so the sort survives except when the sort is the unserved thing.
   */
  it('clears a sort the endpoint cannot serve, so Clear filters is a way out', async () => {
    session.user = grantedUser(NO_SCENE_FILTER);
    failWith = { status: 400, error: 'the metadata endpoint cannot filter scenes by that ordering' };
    await open('/discover/adult?sort=duration&order=desc');

    buttonWithText('Clear filters')?.click();
    await settle();

    expect(router.search).toBe('');
  });
});

describe('hide in library', () => {
  /**
   * The dead end. "Hide in library" is a view over the answer, not a question
   * for the provider, so a page whose every scene is already held renders as
   * empty — and burying Load more inside the results branch meant there was
   * then no way to reach page two at all. The message blamed the provider for
   * it too, which sent the reader to check a setting that was working.
   */
  it('keeps Load more on a page the toggle emptied, and says who emptied it', async () => {
    served = [scene('a', { in_library: true, library_id: 5 })];
    paging = { per_page: 1, total: 9 };
    await open('/discover/adult?hide=1');

    expect(host.textContent).not.toContain('Scene a');
    expect(host.textContent).toContain('already in the library');
    expect(host.textContent).not.toContain('The metadata provider returned nothing');

    const more = buttonWithText('Load more');
    expect(more).toBeDefined();

    served = [scene('b')];
    more?.click();
    await settle();

    expect(host.textContent).toContain('Scene b');
  });

  /** A genuinely empty answer still reads as one. */
  it('still blames the provider when the provider really sent nothing', async () => {
    served = [];
    await open('/discover/adult?hide=1');

    expect(host.textContent).toContain('The metadata provider returned nothing');
  });
});
