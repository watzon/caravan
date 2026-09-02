/**
 * The one dialog both halves of discover go through. What is asserted here is
 * the wiring the pure season maths cannot cover: which controls each mode
 * shows, what the two submit paths actually send, and that an unchecked season
 * ends up unmonitored rather than silently added.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount, type ComponentProps } from 'svelte';
import AddRequestModal from './AddRequestModal.svelte';
import type { DiscoverSeason } from '../api/types';
import type { AddRequestResult } from '../discover';
import { downloads } from '../state/downloads.svelte';
import { tasks } from '../state/tasks.svelte';
import { session } from '../state/session.svelte';
import { system } from '../state/system.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';

interface Call {
  url: string;
  method: string;
  body: Record<string, unknown> | null;
}

function seasons(): DiscoverSeason[] {
  return [
    { season_number: 1, title: 'Season 1', overview: '', poster_url: '', air_date: '2022-02-18', episode_count: 9, in_library: true, requested: false },
    { season_number: 2, title: 'Season 2', overview: '', poster_url: '', air_date: '2023-01-17', episode_count: 10, in_library: false, requested: true },
    { season_number: 3, title: 'Season 3', overview: '', poster_url: '', air_date: '2025-01-17', episode_count: 10, in_library: false, requested: false },
  ];
}

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: Call[];

function stubFetch() {
  calls = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      const body = typeof init?.body === 'string' ? JSON.parse(init.body) : null;
      calls.push({ url, method, body });

      if (url.endsWith('/quality-profiles')) {
        return json({ profiles: [{ id: 4, name: 'HD-1080p' }] });
      }
      if (url.endsWith('/requests') && method === 'POST') {
        if (body?.approve === true) {
          return json({
            request: { id: 11, status: 'approved' },
            series: { id: 42, title: 'Severance' },
            movie: { id: 7, title: 'Blade Runner' },
          }, 201);
        }
        return json({ id: 11, media_type: 'series', tmdb_id: 1396, seasons: [3], status: 'pending' }, 201);
      }
      if (url.includes('/requests/') && url.endsWith('/approve')) {
        // Both keys, so movie and series approvals share the stub; the modal
        // reads the one its mediaType names.
        return json({
          request: { id: 11, status: 'approved' },
          series: { id: 42, title: 'Severance' },
          movie: { id: 7, title: 'Blade Runner' },
        });
      }
      if (url.endsWith('/library/series') && method === 'POST') {
        return json({ id: 42, title: 'Severance' }, 201);
      }
      if (url.endsWith('/library/movies') && method === 'POST') {
        return json({ id: 7, title: 'Blade Runner' }, 201);
      }
      if (url.includes('/downloads')) return json({ downloads: [] });
      if (url.endsWith('/system/status')) {
        return json({
          version: '0',
          mode: 'server',
          storage_root: '',
          schema_version: 1,
          scanning: false,
          counts: { movies: 0, series: 0, media_files: 0, unmatched: 0, wanted: 0 },
          disk_free_bytes: 0,
          disk_total_bytes: 0,
          engine_health: 'ok',
        });
      }
      return json(null, 204);
    }),
  );
}

function json(body: unknown, status = 200): Response {
  if (status === 204) return new Response(null, { status });
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

type ModalProps = ComponentProps<typeof AddRequestModal>;

/** Every test says which mode it is exercising; the rest is a stock series. */
function mountModal(props: Partial<ModalProps> & Pick<ModalProps, 'mode'>) {
  app = mount(AddRequestModal, {
    target: host,
    props: {
      mediaType: 'series' as const,
      tmdbID: 1396,
      title: 'Severance',
      year: 2022,
      posterPath: '/p.jpg',
      seasons: seasons(),
      onclose: () => {},
      ...props,
    },
  }) as Record<string, unknown>;
  flushSync();
}

function checkboxes(): HTMLInputElement[] {
  return [...host.querySelectorAll<HTMLInputElement>('li input[type="checkbox"]')];
}

function primary(): HTMLButtonElement {
  const buttons = [...host.querySelectorAll<HTMLButtonElement>('footer button')];
  return buttons[buttons.length - 1] as HTMLButtonElement;
}

function clickText(text: string) {
  const button = [...host.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.textContent?.trim() === text,
  );
  expect(button, `a "${text}" button`).toBeTruthy();
  button!.click();
  flushSync();
}

async function settle() {
  for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

beforeEach(() => {
  clearToasts();
  stubFetch();
  window.scrollTo = () => {};
  window.localStorage.clear();
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  // A role leaking into the next test would decide which half of the credential
  // copy it reads; null is "not answered for yet", which reads as admin.
  session.user = null;
  system.status = null;
  downloads.stopSoon();
  tasks.stopSoon();
  clearToasts();
  window.localStorage.clear();
  vi.unstubAllGlobals();
});

describe('AddRequestModal — season selection', () => {
  it('opens with the missing, un-requested seasons checked and counts them', () => {
    mountModal({ mode: 'request' });

    const boxes = checkboxes();
    // Season 1 is owned: shown for context, never checkable.
    expect(boxes.map((b) => b.disabled)).toEqual([true, false, false]);
    expect(boxes.map((b) => b.checked)).toEqual([false, false, true]);
    expect(primary().textContent?.trim()).toBe('Request 1 season');
  });

  it('takes every missing season in add mode, including the requested one', () => {
    mountModal({ mode: 'add' });
    expect(checkboxes().map((b) => b.checked)).toEqual([false, true, true]);
    expect(primary().textContent?.trim()).toBe('Add 2 seasons');
  });

  it('honours a preselected season — the per-season Request button', () => {
    mountModal({ mode: 'request', preselect: [2] });
    expect(checkboxes().map((b) => b.checked)).toEqual([false, true, false]);
    expect(primary().textContent?.trim()).toBe('Request 1 season');
  });

  it('flips between select-all and deselect-all, never touching owned seasons', () => {
    mountModal({ mode: 'request' });

    clickText('Select all');
    expect(checkboxes().map((b) => b.checked)).toEqual([false, true, true]);
    expect(primary().textContent?.trim()).toBe('Request 2 seasons');

    clickText('Deselect all');
    expect(checkboxes().map((b) => b.checked)).toEqual([false, false, false]);
    // Nothing checked is nothing to do.
    expect(primary().disabled).toBe(true);
  });

  it('warns only in add mode, and only about checked seasons with a request', () => {
    mountModal({ mode: 'add' });
    expect(host.textContent).toContain(
      'Adding Season 02 will approve its pending request.',
    );

    // Unchecking the requested season removes the warning with it.
    checkboxes()[1]!.click();
    flushSync();
    expect(host.textContent).not.toContain('will approve');
  });

  it('never warns in request mode, where merging is the point', () => {
    mountModal({ mode: 'request', preselect: [2] });
    expect(host.textContent).not.toContain('will approve');
  });
});

describe('AddRequestModal — modes', () => {
  it('offers no profile, folder or search control in request mode', async () => {
    mountModal({ mode: 'request' });
    await settle();

    expect(host.querySelector('#add-profile')).toBeNull();
    expect(host.querySelector('#add-root')).toBeNull();
    expect(host.textContent).not.toContain('Search for selected seasons');
    // Nothing was fetched: a request needs no options.
    expect(calls).toEqual([]);
  });

  it('offers add options and reveals search only after monitoring is enabled', async () => {
    mountModal({ mode: 'add' });
    await settle();

    expect(host.querySelector('#add-profile')).not.toBeNull();
    expect(host.querySelector('#add-root')).not.toBeNull();
    expect(host.textContent).not.toContain('Search for selected seasons right away');
    host.querySelector<HTMLInputElement>('#add-monitored')!.click();
    flushSync();
    expect(host.textContent).toContain('Search for selected seasons right away');
    expect(calls.map((c) => c.url)).toContain('/api/v1/quality-profiles');
  });

  it('gives every input, select, and button an accessible name', async () => {
    mountModal({ mode: 'add' });
    await settle();

    for (const control of host.querySelectorAll<HTMLInputElement | HTMLSelectElement | HTMLButtonElement>(
      'input, select, button',
    )) {
      const id = control.id;
      const name =
        control.getAttribute('aria-label') ||
        (id ? host.querySelector<HTMLLabelElement>(`label[for="${id}"]`)?.textContent?.trim() : '') ||
        control.closest('label')?.textContent?.trim() ||
        (control instanceof HTMLButtonElement ? control.textContent?.trim() : '');
      expect(name, `${control.tagName.toLowerCase()}${id ? `#${id}` : ''}`).toBeTruthy();
    }
  });

  it('exposes the complete storage path when the disabled folder field clips it', async () => {
    const storageRoot = '/Volumes/Media Archive/A Very Long Caravan Library Root';
    system.status = {
      storage_root: storageRoot,
      disk_total_bytes: 100,
      disk_free_bytes: 50,
    } as never;
    mountModal({
      mode: 'add',
      mediaType: 'movie',
      tmdbID: 78,
      title: 'Blade Runner',
      year: 1982,
      seasons: null,
    });
    await settle();

    expect(host.querySelector('#add-root')?.getAttribute('title')).toBe(`${storageRoot} · 50 B free`);
  });

  it('sends the selected profile with an approval before the series search', async () => {
    mountModal({ mode: 'add', requestID: 11 });
    await settle();
    host.querySelector<HTMLInputElement>('#add-monitored')!.click();
    flushSync();

    const select = host.querySelector<HTMLSelectElement>('#add-profile');
    expect(select).not.toBeNull();
    expect(host.querySelector('#add-root')).not.toBeNull();

    select!.value = '4';
    select!.dispatchEvent(new Event('change'));
    flushSync();
    primary().click();
    await settle();

    const writes = calls.filter((call) => call.method !== 'GET');
    expect(writes.map((call) => `${call.method} ${call.url}`)).toEqual([
      'POST /api/v1/requests/11/approve',
      'POST /api/v1/library/series/42/search',
    ]);
    expect(writes[0]?.body).toEqual({ search_now: false, monitored: true, quality_profile_id: 4 });
    expect(calls.filter((call) => call.method === 'PATCH')).toEqual([]);
  });

  it('shows profile loading, an explicit empty state, and no default-only select', async () => {
    let resolveProfileResponse: ((response: Response) => void) | undefined;
    const profileResponse = new Promise<Response>((resolve) => {
      resolveProfileResponse = resolve;
    });
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        if (String(input).endsWith('/quality-profiles')) return profileResponse;
        return json(null, 204);
      }),
    );
    mountModal({ mode: 'add' });

    expect(host.querySelector('[aria-label="Loading quality profiles"]')).not.toBeNull();
    resolveProfileResponse!(json({ profiles: [] }));
    await settle();

    expect(host.querySelector('#add-profile')).toBeNull();
    expect(host.textContent).toContain('No quality profiles are available.');
    expect(host.querySelector('a[href="/settings/quality-profiles"]')).not.toBeNull();
  });

  it('shows a failed profile load with Retry instead of Library default', async () => {
    let profileLoads = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        if (String(input).endsWith('/quality-profiles')) {
          profileLoads += 1;
          return profileLoads === 1
            ? json({ error: 'profiles unavailable' }, 503)
            : json({ profiles: [{ id: 4, name: 'HD-1080p' }] });
        }
        return json(null, 204);
      }),
    );
    mountModal({ mode: 'add' });
    await settle();

    expect(host.textContent).toContain('profiles unavailable');
    expect(host.querySelector('#add-profile')).toBeNull();
    clickText('Retry');
    await settle();

    expect(profileLoads).toBe(2);
    expect(host.querySelector('#add-profile')).not.toBeNull();
  });

  it('opens each new add session with a fresh profile choice and monitoring state', async () => {
    mountModal({ mode: 'add', mediaType: 'movie', tmdbID: 78, title: 'Blade Runner', year: 1982, seasons: null });
    await settle();

    const firstProfile = host.querySelector<HTMLSelectElement>('#add-profile')!;
    firstProfile.value = '4';
    firstProfile.dispatchEvent(new Event('change', { bubbles: true }));
    (host.querySelector<HTMLInputElement>('#add-monitored')!).click();
    flushSync();
    expect(firstProfile.value).toBe('4');
    expect((host.querySelector<HTMLInputElement>('#add-monitored')!).checked).toBe(true);

    unmount(app!);
    app = undefined;
    host.replaceChildren();

    mountModal({ mode: 'add', mediaType: 'movie', tmdbID: 77, title: 'Alien', year: 1979, seasons: null });
    await settle();

    expect(host.querySelector<HTMLSelectElement>('#add-profile')!.value).toBe('0');
    expect((host.querySelector<HTMLInputElement>('#add-monitored')!).checked).toBe(false);
    expect(host.querySelector('#add-availability')).not.toBeNull();
  });
});

describe('AddRequestModal — submitting', () => {
  it('sends a partial season list, and the poster path it was handed', async () => {
    mountModal({ mode: 'request' });
    primary().click();
    await settle();

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.url).toBe('/api/v1/requests');
    expect(post?.body).toEqual({
      media_type: 'series',
      tmdb_id: 1396,
      title: 'Severance',
      year: 2022,
      poster_path: '/p.jpg',
      seasons: [3],
    });
  });

  // Every season the provider knows about is a whole-title request: `seasons`
  // is omitted, which is what the server stores as NULL.
  it('omits seasons entirely when the whole title is asked for', async () => {
    const all = seasons().map((s) => ({ ...s, in_library: false, requested: false }));
    mountModal({ mode: 'request', seasons: all });
    // Nothing is owned or pending, so every season opens checked and the link
    // is already offering to undo that.
    expect(checkboxes().every((b) => b.checked)).toBe(true);
    clickText('Deselect all');
    clickText('Select all');
    primary().click();
    await settle();

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.body).not.toHaveProperty('seasons');
  });

  // A shelf where the library already owns one season can never be a
  // whole-title ask, however hard "Select all" is pressed.
  it('still sends a partial list when a season is already owned', async () => {
    mountModal({ mode: 'request' });
    clickText('Select all');
    primary().click();
    await settle();

    expect((calls.find((c) => c.method === 'POST')?.body ?? {}).seasons).toEqual([2, 3]);
  });

  it('sends no season list for a movie', async () => {
    mountModal({ mode: 'request', mediaType: 'movie', tmdbID: 78, title: 'Blade Runner', year: 1982, seasons: null });
    await settle();
    expect(checkboxes()).toHaveLength(0);
    expect(primary().textContent?.trim()).toBe('Request movie');

    primary().click();
    await settle();

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.body).toEqual({
      media_type: 'movie',
      tmdb_id: 78,
      title: 'Blade Runner',
      year: 1982,
      poster_path: '/p.jpg',
      min_availability: 'released',
    });
  });

  /**
   * An unchecked season has to reach the server as part of the add — otherwise
   * "Add 1 season" would quietly go after all of them, and would close a
   * pending request for seasons nobody went after. Season 1 is already owned,
   * so it stays in the list: the add must not unmonitor what it was not asked
   * about. The search is queued after the add, never as part of it.
   */
  it('adds the series with the seasons it is going after, then searches', async () => {
    mountModal({ mode: 'add' });
    await settle();
    host.querySelector<HTMLInputElement>('#add-monitored')!.click();
    flushSync();
    checkboxes()[1]!.click(); // drop season 2
    flushSync();
    expect(primary().textContent?.trim()).toBe('Add 1 season');

    primary().click();
    await settle();

    const writes = calls.filter((c) => c.method !== 'GET');
    expect(writes.map((c) => `${c.method} ${c.url}`)).toEqual([
      'POST /api/v1/library/series',
      'POST /api/v1/library/series/42/search',
    ]);
    expect(writes[0]?.body).toMatchObject({
      tmdb_id: 1396,
      search_missing: false,
      seasons: [1, 3],
    });
  });

  // Every season checked is a whole-series add: `seasons` is omitted, which is
  // what the endpoint reads as "all of it".
  it('omits seasons when the add covers the whole series', async () => {
    const all = seasons().map((s) => ({ ...s, in_library: false, requested: false }));
    mountModal({ mode: 'add', seasons: all });
    await settle();

    primary().click();
    await settle();

    const post = calls.find((c) => c.method === 'POST' && c.url.endsWith('/library/series'));
    expect(post?.body).not.toHaveProperty('seasons');
  });

  it('routes an approval through the approve endpoint', async () => {
    mountModal({ mode: 'add', requestID: 11, preselect: [2, 3] });
    await settle();
    host.querySelector<HTMLInputElement>('#add-monitored')!.click();
    flushSync();

    primary().click();
    await settle();

    const writes = calls.filter((c) => c.method !== 'GET');
    expect(writes[0]?.url).toBe('/api/v1/requests/11/approve');
    // Season 1 is owned and 2 and 3 were preselected, so the approval grants
    // the whole series and names no seasons. The series search is queued
    // separately after the add, so the approve call never carries it either.
    expect(writes[0]?.body).toEqual({ search_now: false, monitored: true });
    expect(writes.map((c) => c.url)).toContain('/api/v1/library/series/42/search');
  });

  // Granting less than was asked for has to reach the server, or the request
  // is closed for seasons the approver did not hand over.
  it('tells the approve endpoint which seasons it is granting', async () => {
    mountModal({ mode: 'add', requestID: 11, preselect: [2] });
    await settle();

    primary().click();
    await settle();

    const approve = calls.find((c) => c.url.endsWith('/approve'));
    expect(approve?.body).toEqual({ search_now: false, seasons: [1, 2], monitored: false });
  });

  it('sends a chosen quality profile in the first direct add request', async () => {
    mountModal({ mode: 'add', mediaType: 'movie', tmdbID: 78, title: 'Blade Runner', year: 1982, seasons: null });
    await settle();
    host.querySelector<HTMLInputElement>('#add-monitored')!.click();
    flushSync();

    const select = host.querySelector<HTMLSelectElement>('#add-profile')!;
    select.value = '4';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();

    primary().click();
    await settle();

    const writes = calls.filter((call) => call.method !== 'GET');
    expect(writes.map((call) => `${call.method} ${call.url}`)).toEqual([
      'POST /api/v1/library/movies',
    ]);
    expect(writes[0]?.body).toMatchObject({
      tmdb_id: 78,
      search_now: true,
      quality_profile_id: 4,
    });
    expect(calls.filter((call) => call.method === 'PATCH')).toEqual([]);
  });

  it('omits the profile from a direct add on the library default', async () => {
    mountModal({ mode: 'add', mediaType: 'movie', tmdbID: 78, title: 'Blade Runner', year: 1982, seasons: null });
    await settle();

    primary().click();
    await settle();

    const post = calls.find((call) => call.method === 'POST' && call.url.endsWith('/library/movies'));
    expect(post?.body).not.toHaveProperty('quality_profile_id');
    expect(calls.filter((call) => call.method === 'PATCH')).toEqual([]);
  });
  it('lets an admin request and approve a title with the server defaults', async () => {
    session.user = { username: 'admin', role: 'admin', open: false, adult: false };
    let closed = 0;
    let completed: unknown;
    mountModal({
      mode: 'request',
      mediaType: 'movie',
      tmdbID: 78,
      title: 'Blade Runner',
      year: 1982,
      seasons: null,
      onclose: () => (closed += 1),
      ondone: (result: AddRequestResult) => (completed = result),
    });
    await settle();

    clickText('Request and approve');
    await settle();

    expect(calls).toContainEqual({
      url: '/api/v1/requests',
      method: 'POST',
      body: {
        media_type: 'movie',
        tmdb_id: 78,
        title: 'Blade Runner',
        year: 1982,
        poster_path: '/p.jpg',
        min_availability: 'released',
        approve: true,
      },
    });
    expect(toasts.items.map((toast) => toast.message)).toContain('Added Blade Runner.');
    expect(completed).toEqual({ kind: 'added', mediaType: 'movie', libraryID: 7 });
    expect(closed).toBe(1);
    expect(window.location.pathname).toBe('/movies/7');
  });

  it('never offers request and approve to a member', () => {
    session.user = { username: 'housemate', role: 'member', open: false, adult: false };
    mountModal({ mode: 'request' });

    expect([...host.querySelectorAll('button')].some(
      (button) => button.textContent?.trim() === 'Request and approve',
    )).toBe(false);
  });
});


describe('AddRequestModal — minimum availability', () => {
  function availabilitySelect(): HTMLSelectElement | null {
    return host.querySelector('#add-availability');
  }

  function choose(value: string) {
    const select = availabilitySelect();
    expect(select, 'the availability select').toBeTruthy();
    select!.value = value;
    select!.dispatchEvent(new Event('change'));
    flushSync();
  }

  it('offers the select for a movie in both modes, never for a series', async () => {
    mountModal({ mode: 'request', mediaType: 'movie', tmdbID: 78, title: 'Blade Runner', year: 1982, seasons: null });
    await settle();
    expect(availabilitySelect()?.value).toBe('released');
    if (app) unmount(app);
    app = undefined;
    host.replaceChildren();

    mountModal({ mode: 'add', mediaType: 'movie', tmdbID: 78, title: 'Blade Runner', year: 1982, seasons: null });
    await settle();
    expect(availabilitySelect()?.value).toBe('released');
    if (app) unmount(app);
    app = undefined;
    host.replaceChildren();

    mountModal({ mode: 'add' });
    await settle();
    expect(availabilitySelect()).toBeNull();
  });

  it('sends the chosen stage with a movie add', async () => {
    mountModal({ mode: 'add', mediaType: 'movie', tmdbID: 78, title: 'Blade Runner', year: 1982, seasons: null });
    await settle();
    choose('in_cinemas');

    primary().click();
    await settle();

    const post = calls.find((c) => c.url.endsWith('/library/movies') && c.method === 'POST');
    expect(post?.body?.min_availability).toBe('in_cinemas');
  });

  it('opens an approval on the asker\'s choice and passes it through', async () => {
    mountModal({
      mode: 'add', mediaType: 'movie', tmdbID: 78, title: 'Blade Runner', year: 1982,
      seasons: null, requestID: 11, initialAvailability: 'announced',
    });
    await settle();
    expect(availabilitySelect()?.value).toBe('announced');

    primary().click();
    await settle();

    const post = calls.find((c) => c.url.endsWith('/requests/11/approve'));
    expect(post?.body?.min_availability).toBe('announced');
  });

  it('restores monitored independently from minimum availability and submits the explicit value', async () => {
    mountModal({
      mode: 'add',
      mediaType: 'movie',
      tmdbID: 78,
      title: 'Blade Runner',
      year: 1982,
      seasons: null,
      initialMonitored: false,
      initialAvailability: 'announced',
    });
    await settle();

    const monitored = host.querySelector<HTMLInputElement>('#add-monitored');
    expect(monitored).not.toBeNull();
    expect(monitored!.checked).toBe(false);
    expect(availabilitySelect()?.value).toBe('announced');
    monitored!.click();
    flushSync();
    primary().click();
    await settle();

    const post = calls.find((call) => call.method === 'POST' && call.url.endsWith('/library/movies'));
    expect(post?.body).toMatchObject({ monitored: true, min_availability: 'announced' });
  });
});

/**
 * The add path's credential guard.
 *
 * This dialog closes on success and has no empty state to fall back to, so the
 * toast is the only affordance — which makes it all the more important that it
 * names the fix rather than repeating the provider's complaint.
 */
describe('AddRequestModal — metadata credential', () => {
  it('names the fix when the key is missing', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith('/quality-profiles')) return json({ profiles: [] });
        if ((init?.method ?? 'GET') === 'POST') {
          return json(
            { error: 'no metadata provider configured', code: 'metadata_credential_absent' },
            503,
          );
        }
        return json(null, 204);
      }),
    );
    mountModal({ mode: 'add', mediaType: 'movie', seasons: [] });

    primary().click();
    await settle();

    expect(toasts.items.map((t) => t.message).join(' ')).toContain('Settings / Metadata');
  });

  // Discover and Requests both mount this modal without a `seasons` prop, so a
  // series ask prefetches them on mount. That call needs the same credential as
  // the submit below it, and must name the fix rather than raw-toast the
  // provider's complaint. The Requests path is the reachable one: the queue
  // renders without TMDB, so an admin approving a series asked for yesterday
  // meets it the moment the key goes bad.
  it('names the fix when the season prefetch is the call that fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith('/quality-profiles')) return json({ profiles: [] });
        if (url.includes('/discover/series/')) {
          return json(
            { error: 'the TMDB API key was rejected', code: 'metadata_credential_invalid' },
            503,
          );
        }
        return json(null, 204);
      }),
    );
    mountModal({ mode: 'request', mediaType: 'series', seasons: null });

    await settle();

    const said = toasts.items.map((t) => t.message).join(' ');
    expect(said).toContain('Settings / Metadata');
    expect(said).not.toContain('the TMDB API key was rejected');
  });

  // A member is told who can fix it instead: Settings is admin-only, so the
  // destination in the admin copy is a door they cannot open.
  it('tells a member who to ask rather than where to go', async () => {
    session.user = { username: 'housemate', role: 'member', open: false, adult: false };
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith('/quality-profiles')) return json({ profiles: [] });
        if ((init?.method ?? 'GET') === 'POST') {
          return json(
            { error: 'no metadata provider configured', code: 'metadata_credential_absent' },
            503,
          );
        }
        return json(null, 204);
      }),
    );
    mountModal({ mode: 'request', mediaType: 'movie', seasons: [] });

    primary().click();
    await settle();

    const said = toasts.items.map((t) => t.message).join(' ');
    expect(said).toContain('Ask a Caravan admin');
    expect(said).not.toContain('Settings / Metadata');
  });

  it('leaves an unrelated failure in the server’s own words', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith('/quality-profiles')) return json({ profiles: [] });
        if ((init?.method ?? 'GET') === 'POST') {
          return json({ error: 'already in the library' }, 409);
        }
        return json(null, 204);
      }),
    );
    mountModal({ mode: 'add', mediaType: 'movie', seasons: [] });

    primary().click();
    await settle();

    const said = toasts.items.map((t) => t.message).join(' ');
    expect(said).toContain('already in the library');
    expect(said).not.toContain('Settings / Metadata');
  });
});
