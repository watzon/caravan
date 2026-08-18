/**
 * Acquisition actions live with the file state they change. Automatic search
 * queues through the API, while choosing a release stays a link to the picker.
 * A queued count of zero is reported honestly.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import MovieDetail from './MovieDetail.svelte';
import { tasks } from '../state/tasks.svelte';
import { session } from '../state/session.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';

const MOVIE = {
  id: 7,
  tmdb_id: 438631,
  imdb_id: '',
  title: 'Dune',
  sort_title: 'dune',
  year: 2021,
  overview: '',
  path: '',
  poster_path: '',
  poster_url: '',
  monitored: true,
  quality_profile_id: 0,
  library_id: 1,
  release_date: '',
  min_availability: 'released',
  added_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  file: null,
};

const PROFILES = [
  {
    id: 1,
    name: 'Balanced',
    cutoff: '1080p',
    items: ['1080p'],
    upgrade_allowed: true,
    tv_profile: 'safe',
    is_default: true,
    assignments: { libraries: 0, movies: 0, series: 0 },
    created_at: '',
    updated_at: '',
  },
  {
    id: 2,
    name: 'Archive',
    cutoff: '2160p',
    items: ['2160p', '1080p'],
    upgrade_allowed: true,
    tv_profile: 'capable',
    is_default: false,
    assignments: { libraries: 0, movies: 0, series: 0 },
    created_at: '',
    updated_at: '',
  },
];

const LIBRARIES = [
  {
    id: 1,
    kind: 'movie',
    name: 'Cinema',
    root_path: 'movies',
    dlna_visible: true,
    route_torrent: '',
    route_usenet: '',
    quality_profile_id: 2,
    indexers: [],
  },
];

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: { url: string; method: string; body: unknown; signal?: AbortSignal }[];

// A movie with an imported file: the delete-files checkbox is only offered
// when there is something on disk to delete.
const MOVIE_WITH_FILE = {
  ...MOVIE,
  file: {
    id: 1,
    path: 'library/Movies/Dune (2021)/Dune (2021).mkv',
    size: 1,
    quality: '1080p',
    source: 'bluray',
    codec: 'x265',
    audio: '',
    release_group: '',
    added_at: '2026-01-01T00:00:00Z',
    modified_at: '2026-01-01T00:00:00Z',
    compatibility: { verdict: 'ok', reasons: [] },
  },
};

function stubFetch(
  queued: number,
  movie: unknown = MOVIE,
  assignedMovie: unknown = movie,
  profileChoicesStatus = 200,
  discover: unknown = { cast: [] },
  discoverStatus = 200,
) {
  calls = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const body = typeof init?.body === 'string' ? JSON.parse(init.body) : null;
      calls.push({
        url,
        method: init?.method ?? 'GET',
        body,
        signal: init?.signal ?? undefined,
      });
      if (url.endsWith('/quality-profiles')) {
        return new Response(
          JSON.stringify(
            profileChoicesStatus === 200 ? { profiles: PROFILES } : { error: 'Profiles unavailable' },
          ),
          {
            status: profileChoicesStatus,
            headers: { 'Content-Type': 'application/json' },
          },
        );
      }
      if (url.endsWith('/libraries')) {
        return new Response(JSON.stringify({ libraries: LIBRARIES }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (url.includes('/discover/movie/')) {
        return new Response(JSON.stringify(discover), {
          status: discoverStatus,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (url.endsWith('/search')) {
        return new Response(JSON.stringify({ queued }), {
          status: 202,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (body?.quality_profile_id !== undefined) {
        return new Response(JSON.stringify(assignedMovie), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (body?.monitored !== undefined) {
        return new Response(JSON.stringify({ ...(movie as object), monitored: body.monitored }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return new Response(JSON.stringify(movie), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
}

beforeEach(() => {
  clearToasts();
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  clearToasts();
  tasks.stopSoon();
  session.forget();
  vi.unstubAllGlobals();
});

async function settle() {
  await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

function searchNowButton(): HTMLButtonElement {
  const button = [...host.querySelectorAll('button')].find((b) =>
    b.textContent?.includes('Search now'),
  );
  expect(button, 'Search now button').toBeDefined();
  return button as HTMLButtonElement;
}

/** The header's ⋯ trigger, named for a screen reader by the movie title. */
function menuTrigger(): HTMLButtonElement {
  const button = [...host.querySelectorAll('button')].find(
    (b) => b.getAttribute('aria-label') === 'More actions for Dune',
  );
  expect(button, 'overflow menu trigger').toBeDefined();
  return button as HTMLButtonElement;
}

/**
 * The Remove item, with its menu opened. Removal moved behind the ⋯ so it is no
 * longer one mis-click from the search buttons; everything it does after the
 * click is unchanged, which is why every assertion below this line is.
 */
function removeTrigger(): HTMLButtonElement {
  menuTrigger().click();
  flushSync();
  const item = [...host.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find((b) =>
    b.textContent?.includes('Remove'),
  );
  expect(item, 'Remove trigger').toBeDefined();
  return item as HTMLButtonElement;
}

function editButton(): HTMLButtonElement {
  const button = [...host.querySelectorAll<HTMLButtonElement>('button')].find(
    (candidate) => candidate.textContent?.trim() === 'Edit',
  );
  expect(button, 'Edit button').toBeDefined();
  return button as HTMLButtonElement;
}

async function openEditor(): Promise<HTMLElement> {
  editButton().click();
  await settle();
  const dialog = host.querySelector<HTMLElement>('[role="dialog"]');
  expect(dialog, 'edit dialog').not.toBeNull();
  return dialog!;
}

function saveChangesButton(dialog: HTMLElement): HTMLButtonElement {
  const button = [...dialog.querySelectorAll<HTMLButtonElement>('button')].find(
    (candidate) => candidate.textContent?.trim() === 'Save changes',
  );
  expect(button, 'Save changes button').toBeDefined();
  return button!;
}

/** The confirm dialog's own Remove button, whose label is exactly "Remove". */
function confirmButton(): HTMLButtonElement {
  const button = [...host.querySelectorAll('button')].find(
    (b) => b.textContent?.trim() === 'Remove',
  );
  expect(button, 'confirm Remove button').toBeDefined();
  return button as HTMLButtonElement;
}

function deletes(): { url: string; method: string }[] {
  return calls.filter((c) => c.method === 'DELETE').map(({ url, method }) => ({ url, method }));
}

describe('MovieDetail back link', () => {
  it('returns to the library that owns the movie, not the movies kind root', async () => {
    session.user = {
      username: 'root',
      role: 'admin',
      open: false,
      adult: false,
      libraries: [
        { id: 4, kind: 'anime', name: 'Anime', slug: 'anime', icon: '' },
        { id: 1, kind: 'movie', name: 'Movies', slug: 'movies', icon: '' },
      ],
    };
    stubFetch(0, { ...MOVIE, library_id: 4 });
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    const back = host.querySelector('a');
    expect(back?.getAttribute('href')).toBe('/l/anime');
    expect(back?.textContent).toContain('Anime');
  });
});

describe('MovieDetail facts', () => {
  it('renders one detail list without repeating the hero facts', async () => {
    stubFetch(0, { ...MOVIE, release_date: '2021-10-22' });
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    const heroFacts = host.querySelector('h2')?.parentElement?.nextElementSibling;
    expect(heroFacts?.textContent).toContain('2021');
    expect(heroFacts?.textContent).toContain('Released');

    const labels = [...host.querySelectorAll('dt')].map((term) => term.textContent?.trim());
    expect(host.querySelectorAll('dl')).toHaveLength(1);
    expect(labels).toEqual(['Folder', 'TMDB id', 'Added']);
    expect(new Set(labels).size).toBe(labels.length);
    expect(labels).not.toEqual(expect.arrayContaining(['Year', 'Status', 'Release date']));
    expect(host.querySelector('select')).toBeNull();
  });
  it('keeps full folder and file paths on truncated values', async () => {
    const folder = 'library/Movies/Dune (2021)/A deliberately long folder name';
    const filePath = `${folder}/Dune (2021) Remux Extended Edition.mkv`;
    stubFetch(0, {
      ...MOVIE_WITH_FILE,
      path: folder,
      file: { ...MOVIE_WITH_FILE.file, path: filePath },
    });
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    const folderValue = [...host.querySelectorAll('dt')].find(
      (term) => term.textContent?.trim() === 'Folder',
    )?.nextElementSibling as HTMLElement | undefined;
    expect(folderValue?.classList.contains('truncate')).toBe(true);
    expect(folderValue?.title).toBe(folder);

    const fileCell = [...host.querySelectorAll<HTMLElement>('td[title]')].find(
      (cell) => cell.title === filePath,
    );
    expect(fileCell?.textContent?.trim()).not.toBe(filePath);
    expect(fileCell?.title).toBe(filePath);
  });
});

describe('MovieDetail cast', () => {
  it('loads cast by the movie TMDB id and preserves full truncated text in tooltips', async () => {
    const name = 'Rebecca Very-Long-Name Ferguson';
    const character = 'Lady Jessica of House Atreides';
    stubFetch(0, MOVIE, MOVIE, 200, {
      cast: [{ tmdb_id: 933238, name, character, profile_url: '' }],
    });
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    expect(
      calls.filter((call) => call.url.includes('/discover/')).map((call) => call.url),
    ).toEqual(['/api/v1/discover/movie/438631']);

    const section = host.querySelector('#movie-cast-heading')?.closest('section');
    expect(section?.textContent).toContain('Cast');
    const [renderedName, renderedCharacter] =
      section?.querySelectorAll<HTMLElement>('li [title]') ?? [];
    expect(renderedName?.textContent?.trim()).toBe(name);
    expect(renderedName?.classList.contains('truncate')).toBe(true);
    expect(renderedName?.title).toBe(name);
    expect(renderedCharacter?.textContent?.trim()).toBe(character);
    expect(renderedCharacter?.classList.contains('truncate')).toBe(true);
    expect(renderedCharacter?.title).toBe(character);
  });

  it.each([
    { condition: 'empty', response: { cast: [] }, status: 200 },
    { condition: 'failing', response: { error: 'Metadata unavailable' }, status: 503 },
  ])(
    'keeps the library movie visible when cast metadata is $condition',
    async ({ response, status }) => {
      stubFetch(0, MOVIE, MOVIE, 200, response, status);
      app = mount(MovieDetail, { target: host, props: { id: 7 } });
      await settle();

      expect(host.querySelector('h2')?.textContent).toContain('Dune');
      expect(host.querySelector('#movie-cast-heading')).toBeNull();
      expect(toasts.items).toHaveLength(0);
    },
  );

  it('does not ask Discover for a movie without a positive TMDB id', async () => {
    stubFetch(0, { ...MOVIE, tmdb_id: 0 });
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    expect(calls.some((call) => call.url.includes('/discover/'))).toBe(false);
    expect(host.querySelector('h2')?.textContent).toContain('Dune');
  });

  it('aborts the supplemental request when the detail route unmounts', async () => {
    stubFetch(0);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    const signal = calls.find((call) => call.url.includes('/discover/'))?.signal;
    expect(signal?.aborted).toBe(false);
    unmount(app);
    app = undefined;
    expect(signal?.aborted).toBe(true);
  });
});

describe('MovieDetail remove', () => {
  it('untracks without touching files when the checkbox is left alone', async () => {
    stubFetch(0);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    removeTrigger().click();
    await settle();
    expect(deletes(), 'opening the confirm must not delete anything').toEqual([]);

    confirmButton().click();
    await settle();

    // No ?files=true: the files stay on disk and a rescan would re-add them.
    expect(deletes()).toEqual([{ url: '/api/v1/library/movies/7', method: 'DELETE' }]);
    expect(toasts.items[0]!.message).toContain('Removed Dune');
  });

  it('offers no delete-files checkbox when the movie has no file', async () => {
    stubFetch(0);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    removeTrigger().click();
    await settle();

    expect(host.querySelector('input[type="checkbox"]')).toBeNull();
  });

  it('deletes the files when the checkbox is ticked', async () => {
    stubFetch(0, MOVIE_WITH_FILE);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    removeTrigger().click();
    await settle();

    const checkbox = host.querySelector<HTMLInputElement>('input[type="checkbox"]');
    expect(checkbox, 'delete-files checkbox').toBeTruthy();
    checkbox!.checked = true;
    checkbox!.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();

    confirmButton().click();
    await settle();

    expect(deletes()).toEqual([
      { url: '/api/v1/library/movies/7?files=true', method: 'DELETE' },
    ]);
  });

  it('sends nothing when the confirm is cancelled', async () => {
    stubFetch(0);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    removeTrigger().click();
    await settle();

    const cancel = [...host.querySelectorAll('button')].find(
      (b) => b.textContent?.trim() === 'Cancel',
    );
    expect(cancel, 'Cancel button').toBeDefined();
    cancel!.click();
    await settle();

    expect(deletes()).toEqual([]);
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });
});

describe('MovieDetail search actions', () => {
  it('puts automatic and release-picker actions inside the empty file state', async () => {
    stubFetch(1);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    const fileSection = [...host.querySelectorAll('h3')].find(
      (heading) => heading.textContent?.trim() === 'File',
    )?.closest('section');
    expect(fileSection).not.toBeNull();
    expect(fileSection?.contains(searchNowButton())).toBe(true);

    const picker = host.querySelector('a[href="/movies/7/search"]');
    expect(picker?.textContent).toContain('Choose a release');
    expect(fileSection?.contains(picker)).toBe(true);

    searchNowButton().click();
    await settle();

    expect(calls.filter((c) => c.method === 'POST').map(({ url, method }) => ({ url, method }))).toEqual([
      { url: '/api/v1/library/movies/7/search', method: 'POST' },
    ]);
    expect(toasts.items).toHaveLength(0);
  });

  it('says nothing was queued when the movie is not searchable', async () => {
    stubFetch(0);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    searchNowButton().click();
    await settle();

    expect(toasts.items).toHaveLength(1);
    expect(toasts.items[0]!.message).toContain('Nothing to search');
    expect(toasts.items[0]!.tone).toBe('info');
  });

  it('monitors an unmonitored movie before starting its automatic search', async () => {
    stubFetch(1, { ...MOVIE, monitored: false });
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    expect(host.textContent).toContain('Monitor this movie to search automatically');
    expect([...host.querySelectorAll('button')].some((button) =>
      button.textContent?.includes('Search now'),
    )).toBe(false);
    const monitorAndSearch = [...host.querySelectorAll<HTMLButtonElement>('button')].find(
      (button) => button.textContent?.includes('Monitor and search'),
    );
    expect(monitorAndSearch).toBeDefined();

    monitorAndSearch!.click();
    await settle();

    expect(calls.filter((call) => call.method === 'PATCH' || call.method === 'POST')).toEqual([
      {
        url: '/api/v1/library/movies/7',
        method: 'PATCH',
        body: { monitored: true },
        signal: undefined,
      },
      {
        url: '/api/v1/library/movies/7/search',
        method: 'POST',
        body: null,
        signal: undefined,
      },
    ]);
  });

  it('replaces search controls with a queue action while downloading', async () => {
    stubFetch(0, { ...MOVIE, downloading: true });
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    expect(host.textContent).toContain('Download in progress');
    expect(host.querySelector('a[href="/queue"]')?.textContent).toContain('View queue');
    expect(host.querySelector('a[href="/movies/7/search"]')).toBeNull();
    expect([...host.querySelectorAll('button')].some((button) =>
      button.textContent?.includes('Search now'),
    )).toBe(false);
  });

  it('offers an explicit replacement search beside an imported file', async () => {
    stubFetch(0, MOVIE_WITH_FILE);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    const replacement = host.querySelector('a[href="/movies/7/search"]');
    expect(replacement?.textContent).toContain('Choose another release');
    expect([...host.querySelectorAll('button')].some((button) =>
      button.textContent?.includes('Search now'),
    )).toBe(false);
  });
});

describe('minimum availability', () => {
  it('edits the stored stage in the modal and saves it with the other settings', async () => {
    stubFetch(0);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    const dialog = await openEditor();
    const select = dialog.querySelector<HTMLSelectElement>(
      'select[aria-label="Minimum availability"]',
    );
    expect(select).not.toBeNull();
    expect(select!.value).toBe('released');

    select!.value = 'in_cinemas';
    select!.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();
    expect(calls.some((call) => call.method === 'PATCH')).toBe(false);

    saveChangesButton(dialog).click();
    await settle();

    const patch = calls.find((c) => c.method === 'PATCH');
    expect(patch?.url.endsWith('/library/movies/7')).toBe(true);
    expect(patch?.body).toEqual({
      monitored: true,
      quality_profile_id: 0,
      min_availability: 'in_cinemas',
    });
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });
});

describe('quality profile assignment', () => {
  it('shows the derived playback target and saves an item override on submit', async () => {
    const assignedMovie = { ...MOVIE, quality_profile_id: 1 };
    stubFetch(0, MOVIE, assignedMovie);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    const dialog = await openEditor();
    const select = dialog.querySelector<HTMLSelectElement>(
      'select[aria-label="Quality profile"]',
    );
    expect(select).not.toBeNull();
    expect(select!.value).toBe('0');
    expect(dialog.textContent).toContain('Capable');

    select!.value = '1';
    select!.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();
    expect(dialog.textContent).toContain('Safe');
    expect(dialog.textContent).toContain('This title overrides its library with Balanced.');

    saveChangesButton(dialog).click();
    await settle();

    expect(
      calls.find(
        (call) => (call.body as { quality_profile_id?: number } | null)?.quality_profile_id === 1,
      ),
    ).toMatchObject({
      url: '/api/v1/library/movies/7',
      method: 'PATCH',
      body: {
        monitored: true,
        quality_profile_id: 1,
        min_availability: 'released',
      },
    });
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });
  it('keeps the detail visible when profile choices cannot load', async () => {
    stubFetch(0, MOVIE, MOVIE, 500);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    expect(host.textContent).toContain('Dune');
    expect(host.querySelector('[role="alert"]')).toBeNull();

    const dialog = await openEditor();
    expect(dialog.querySelector('[role="alert"]')?.textContent).toContain(
      'Could not load title settings: Profiles unavailable',
    );
    expect(
      [...dialog.querySelectorAll('button')].some((button) => button.textContent?.trim() === 'Retry'),
    ).toBe(true);
  });
});

/**
 * Title controls open one editor. Changes stay local until Save, while Remove
 * remains behind the overflow menu.
 */
describe('MovieDetail title controls', () => {
  it('keeps Edit and overflow beside the title', async () => {
    stubFetch(0);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    const titleControls = host.querySelector('h2')?.parentElement;
    expect(titleControls?.contains(editButton())).toBe(true);
    expect(titleControls?.contains(menuTrigger())).toBe(true);
  });

  it('saves monitoring through the edit modal', async () => {
    stubFetch(0);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    const dialog = await openEditor();
    const monitor = dialog.querySelector<HTMLButtonElement>('button[role="switch"]');
    expect(monitor?.getAttribute('aria-checked')).toBe('true');
    monitor!.click();
    await settle();
    saveChangesButton(dialog).click();
    await settle();

    const patch = calls.find((c) => c.method === 'PATCH');
    expect(patch?.url).toBe('/api/v1/library/movies/7');
    expect(patch?.body).toEqual({
      monitored: false,
      quality_profile_id: 0,
      min_availability: 'released',
    });
  });

  it('keeps unchanged settings from being submitted', async () => {
    stubFetch(0);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    const dialog = await openEditor();
    expect(saveChangesButton(dialog).disabled).toBe(true);
    expect(dialog.querySelector('button[role="switch"]')?.getAttribute('aria-checked')).toBe('true');
  });

  it('closes the menu on Escape without removing anything', async () => {
    stubFetch(0);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    menuTrigger().click();
    flushSync();
    expect(host.querySelector('[role="menu"]'), 'the menu is open').not.toBeNull();

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    flushSync();
    expect(host.querySelector('[role="menu"]')).toBeNull();
    expect(deletes()).toEqual([]);
  });

  it('keeps the red trash button out of the row', async () => {
    stubFetch(0);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    const row = [...host.querySelectorAll('button')].map((b) => b.className).join(' ');
    expect(row).not.toContain('bg-danger');
  });
});
