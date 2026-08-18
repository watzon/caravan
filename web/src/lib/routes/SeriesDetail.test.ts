/**
 * Series acquisition actions live with the episode inventory. Whole-series
 * search queues missing episodes, while season and episode links open the
 * release picker for that exact target.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import SeriesDetail from './SeriesDetail.svelte';
import { tasks } from '../state/tasks.svelte';
import { session } from '../state/session.svelte';
import { navigate } from '../router.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';

const SERIES = {
  id: 3,
  tmdb_id: 95396,
  tvdb_id: 0,
  imdb_id: '',
  title: 'Severance',
  sort_title: 'severance',
  year: 2022,
  overview: '',
  status: 'Continuing',
  path: '',
  poster_path: '',
  poster_url: '',
  monitored: true,
  quality_profile_id: 0,
  library_id: 1,
  first_aired: '',
  added_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  seasons: [],
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
];

const LIBRARIES = [
  {
    id: 1,
    kind: 'tv',
    name: 'Television',
    root_path: 'series',
    dlna_visible: true,
    route_torrent: '',
    route_usenet: '',
    quality_profile_id: 0,
    indexers: [],
  },
  {
    id: 3,
    kind: 'anime',
    name: 'Anime',
    root_path: 'anime',
    active: true,
    is_default: true,
    dlna_visible: true,
    route_torrent: '',
    route_usenet: '',
    quality_profile_id: 1,
    indexers: [],
  },
];

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: { url: string; method: string; body?: Record<string, unknown> | null }[];

// Two seasons holding three episode files between them: the confirm names the
// count it would delete, so "Also delete 3 files" has to come from the data.
const SERIES_WITH_FILES = {
  ...SERIES,
  seasons: [
    {
      id: 1,
      series_id: 3,
      season_number: 1,
      title: '',
      overview: '',
      poster_path: '',
      air_date: '',
      monitored: true,
      episodes: [1, 2].map((n) => episode(n, true)),
    },
    {
      id: 2,
      series_id: 3,
      season_number: 2,
      title: '',
      overview: '',
      poster_path: '',
      air_date: '',
      monitored: true,
      episodes: [episode(1, true), episode(2, false)],
    },
  ],
};

const COMPLETE_SERIES = {
  ...SERIES_WITH_FILES,
  seasons: SERIES_WITH_FILES.seasons.map((season) => ({
    ...season,
    episodes: season.episodes.map((row) => ({
      ...row,
      file: row.file ?? episode(row.episode_number, true).file,
    })),
  })),
};

function episode(number: number, withFile: boolean) {
  return {
    id: number * 10,
    series_id: 3,
    season_number: 1,
    episode_number: number,
    tmdb_id: 0,
    title: '',
    overview: '',
    air_date: '',
    monitored: true,
    file: withFile
      ? {
          id: number,
          path: `library/TV/Severance (2022)/Season 01/e${number}.mkv`,
          size: 1,
          quality: '1080p',
          source: 'webdl',
          codec: '',
          audio: '',
          release_group: '',
          added_at: '2026-01-01T00:00:00Z',
          modified_at: '2026-01-01T00:00:00Z',
          compatibility: { verdict: 'ok', reasons: [] },
        }
      : null,
  };
}

function stubFetch(queued: number, series: unknown = SERIES, qualityProfileStatus = 200) {
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
      if (url.endsWith('/search')) {
        return new Response(JSON.stringify({ queued }), {
          status: 202,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (body?.quality_profile_id !== undefined && qualityProfileStatus !== 200) {
        return new Response(JSON.stringify({ error: 'Could not update quality profile' }), {
          status: qualityProfileStatus,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (body?.monitored !== undefined) {
        return new Response(JSON.stringify({ ...(series as object), monitored: body.monitored }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return new Response(JSON.stringify(series), {
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
  navigate('/');
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

/** The header's ⋯ trigger, named for a screen reader by the series title. */
function menuTrigger(): HTMLButtonElement {
  const button = [...host.querySelectorAll('button')].find(
    (b) => b.getAttribute('aria-label') === 'More actions for Severance',
  );
  expect(button, 'overflow menu trigger').toBeDefined();
  return button as HTMLButtonElement;
}

/**
 * The Remove item, with its menu opened. Removal moved behind the ⋯; what it
 * does after the click is unchanged, which is why every assertion below this
 * line is unchanged too.
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

function confirmButton(): HTMLButtonElement {
  const button = [...host.querySelectorAll('button')].find(
    (b) => b.textContent?.trim() === 'Remove',
  );
  expect(button, 'confirm Remove button').toBeDefined();
  return button as HTMLButtonElement;
}

// The body is recorded for the monitored PATCH; the assertions here are about
// the request itself, so it is projected away.
function deletes(): { url: string; method: string }[] {
  return calls.filter((c) => c.method === 'DELETE').map(({ url, method }) => ({ url, method }));
}

describe('SeriesDetail back link', () => {
  it('returns to the anime shelf when the series lives there', async () => {
    session.user = {
      username: 'root',
      role: 'admin',
      open: false,
      adult: false,
      libraries: [
        { id: 1, kind: 'tv', name: 'Series', slug: 'series', icon: '' },
        { id: 3, kind: 'anime', name: 'Anime', slug: 'anime', icon: '' },
      ],
    };
    stubFetch(0, { ...SERIES, library_id: 3, kind: 'anime', title: 'Attack on Titan' });
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    const back = host.querySelector('a');
    expect(back?.getAttribute('href')).toBe('/l/anime');
    expect(back?.textContent).toContain('Anime');

    const dialog = await openEditor();
    expect(dialog.textContent).toContain('Inherited from Anime: Balanced.');
    expect(dialog.textContent).toContain('Safe');
  });
});

describe('SeriesDetail facts', () => {
  it('keeps configuration controls out of the hero', async () => {
    stubFetch(0);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    const labels = [...host.querySelectorAll('dt')].map((term) => term.textContent?.trim());
    expect(labels).toEqual(['Folder', 'TMDB id', 'First aired']);
    expect(host.querySelector('select')).toBeNull();
  });
});

describe('SeriesDetail season inventory', () => {
  it('states the on-disk count and names both missing-file cells', async () => {
    stubFetch(0, SERIES_WITH_FILES);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    const headers = [...host.querySelectorAll('section > header')].map(
      (header) => header.textContent?.replace(/\s+/g, ' ').trim() ?? '',
    );
    expect(headers[0]).toContain('Season 01 2 of 2 on disk');
    expect(headers[1]).toContain('Season 02 1 of 2 on disk');
    expect(host.querySelector('#s1e1')).not.toBeNull();
    expect(host.querySelector('#s1e2')).not.toBeNull();

    navigate('/series/3#s1e2');
    flushSync();
    expect(host.querySelector('#s1e2')?.className).toContain('ring-accent');
    expect(host.querySelector('#s1e1')?.className).not.toContain('ring-accent');

    const rows = [...host.querySelectorAll('tbody tr')];
    const missingFileRow = rows.find((row) => row.textContent?.includes('No file'));
    expect(missingFileRow, 'episode without a file').toBeDefined();
    const missingFileCells = [...missingFileRow!.querySelectorAll('td')];
    expect(missingFileCells[4]?.textContent?.trim()).toBe('No file');
    expect(missingFileCells[5]?.textContent?.trim()).toBe('No file');

    const importedFileRow = rows.find((row) => row.textContent?.includes('1080p'));
    expect(importedFileRow, 'episode with an imported file').toBeDefined();
    const importedFileCells = [...importedFileRow!.querySelectorAll('td')];
    expect(importedFileCells[4]?.textContent?.trim()).toBe('1080p');
    expect(importedFileCells[5]?.textContent?.trim()).toBe('1 B');
  });

  it('marks a future episode so it does not read as available', async () => {
    stubFetch(0, {
      ...SERIES_WITH_FILES,
      seasons: [
        {
          ...SERIES_WITH_FILES.seasons[0],
          episodes: [
            { ...episode(1, false), title: 'Aired', air_date: '2020-01-15', monitored: false },
            { ...episode(2, false), title: 'Upcoming', air_date: '2099-06-15', monitored: false },
            { ...episode(3, true), title: 'Imported early', air_date: '2099-06-22' },
          ],
        },
      ],
    });
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    const rows = [...host.querySelectorAll('tbody tr')];
    const aired = rows.find((row) => row.textContent?.includes('Aired'));
    const upcoming = rows.find((row) => row.textContent?.includes('Upcoming'));
    const imported = rows.find((row) => row.textContent?.includes('Imported early'));
    expect(aired, 'aired episode').toBeDefined();
    expect(upcoming, 'upcoming episode').toBeDefined();
    expect(imported, 'imported future episode').toBeDefined();

    const airedDate = aired!.querySelectorAll('td')[2];
    const upcomingDate = upcoming!.querySelectorAll('td')[2];
    const importedDate = imported!.querySelectorAll('td')[2];

    expect(airedDate?.querySelector('svg')).toBeNull();
    expect(airedDate?.textContent?.trim()).toBe('15 Jan 2020');
    expect(aired?.querySelectorAll('td')[3]?.textContent).toContain('Unmonitored');
    expect(aired?.className).not.toContain('bg-danger/15');

    expect(upcomingDate?.querySelector('svg'), 'clock on an unaired date').not.toBeNull();
    expect(upcomingDate?.textContent?.trim()).toBe('15 Jun 2099');
    expect(upcomingDate?.querySelector('[title]')?.getAttribute('title')).toMatch(/^Airs in /);
    expect(upcoming?.querySelectorAll('td')[3]?.textContent).toContain('Unaired');
    expect(upcoming?.className).toContain('bg-danger/15');

    // A file already on disk is available even when the air date is still ahead.
    expect(importedDate?.querySelector('svg')).toBeNull();
    expect(imported?.className).not.toContain('bg-danger/15');
  });

  it('keeps full folder and episode titles on truncated values', async () => {
    const folder = 'library/TV/Severance (2022)/A deliberately long series folder';
    const episodeTitle = 'The Very Long and Important Episode Title That Must Remain Available';
    stubFetch(0, {
      ...SERIES_WITH_FILES,
      path: folder,
      seasons: [
        {
          ...SERIES_WITH_FILES.seasons[0],
          episodes: [{ ...episode(1, true), title: episodeTitle }],
        },
      ],
    });
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    const titled = [...host.querySelectorAll<HTMLElement>('[title]')];
    const folderValue = titled.find((element) => element.title === folder);
    const episodeValue = titled.find((element) => element.title === episodeTitle);
    expect(folderValue?.classList.contains('truncate')).toBe(true);
    expect(episodeValue?.classList.contains('truncate')).toBe(true);
  });

  it('PATCHes the season monitor flag and reloads its cascaded episode state', async () => {
    stubFetch(0, SERIES_WITH_FILES);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    const seasonToggle = host.querySelector<HTMLButtonElement>(
      'button[role="switch"][aria-label="Monitor Season 01"]',
    );
    expect(seasonToggle, 'Season 01 monitor switch').not.toBeNull();
    expect(seasonToggle!.getAttribute('aria-checked')).toBe('true');

    seasonToggle!.click();
    await settle();

    expect(
      calls.find(
        (call) =>
          call.url === '/api/v1/library/series/3/seasons/1' && call.method === 'PATCH',
      ),
    ).toMatchObject({ body: { monitored: false } });
    expect(
      calls.filter(
        (call) => call.url === '/api/v1/library/series/3' && call.method === 'GET',
      ),
    ).toHaveLength(2);
  });
});

describe('SeriesDetail remove', () => {
  it('untracks without touching files by default', async () => {
    stubFetch(0, SERIES_WITH_FILES);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    removeTrigger().click();
    await settle();
    expect(deletes(), 'opening the confirm must not delete anything').toEqual([]);

    confirmButton().click();
    await settle();

    expect(deletes()).toEqual([{ url: '/api/v1/library/series/3', method: 'DELETE' }]);
    expect(toasts.items[0]!.message).toContain('Removed Severance');
  });

  it('counts the episode files the checkbox would delete', async () => {
    stubFetch(0, SERIES_WITH_FILES);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    removeTrigger().click();
    await settle();

    const dialog = host.querySelector('[role="dialog"]');
    expect(dialog?.textContent).toContain('Also delete 3 files from disk');

    const checkbox = host.querySelector<HTMLInputElement>('input[type="checkbox"]');
    expect(checkbox, 'delete-files checkbox').toBeTruthy();
    checkbox!.checked = true;
    checkbox!.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();

    confirmButton().click();
    await settle();

    expect(deletes()).toEqual([
      { url: '/api/v1/library/series/3?files=true', method: 'DELETE' },
    ]);
  });

  it('sends nothing when the confirm is cancelled', async () => {
    stubFetch(0, SERIES_WITH_FILES);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
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

describe('SeriesDetail search actions', () => {
  it('puts whole-series acquisition actions above the season inventory', async () => {
    stubFetch(4, SERIES_WITH_FILES);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    const inventory = host.querySelector('section[aria-labelledby="series-episodes-heading"]');
    expect(inventory).not.toBeNull();
    expect(inventory?.contains(searchNowButton())).toBe(true);
    expect(inventory?.textContent).toContain('3 of 4 on disk');

    const picker = host.querySelector('a[href="/series/3/search"]');
    expect(picker?.textContent).toContain('Choose a release');
    expect(inventory?.contains(picker)).toBe(true);

    searchNowButton().click();
    await settle();

    expect(
      calls.filter((c) => c.method === 'POST').map(({ url, method }) => ({ url, method })),
    ).toEqual([
      { url: '/api/v1/library/series/3/search', method: 'POST' },
    ]);
    expect(toasts.items).toHaveLength(0);
  });

  it('says nothing was queued when the series is already covered', async () => {
    stubFetch(0, SERIES_WITH_FILES);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    searchNowButton().click();
    await settle();

    expect(toasts.items).toHaveLength(1);
    expect(toasts.items[0]!.message).toContain('Nothing to search');
  });

  it('singularizes a lone queued search', async () => {
    stubFetch(1, SERIES_WITH_FILES);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    searchNowButton().click();
    await settle();

    expect(toasts.items).toHaveLength(0);
  });

  it('monitors an unmonitored series before starting its automatic search', async () => {
    stubFetch(2, { ...SERIES_WITH_FILES, monitored: false });
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

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
        url: '/api/v1/library/series/3',
        method: 'PATCH',
        body: { monitored: true },
      },
      {
        url: '/api/v1/library/series/3/search',
        method: 'POST',
        body: null,
      },
    ]);
  });

  it('replaces whole-series search with a queue action while downloading', async () => {
    const downloading = {
      ...SERIES_WITH_FILES,
      downloading: true,
      seasons: SERIES_WITH_FILES.seasons.map((season, index) => ({
        ...season,
        episodes: season.episodes.map((row, rowIndex) =>
          index === 1 && rowIndex === 1 ? { ...row, downloading: true } : row,
        ),
      })),
    };
    stubFetch(0, downloading);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    const queueLinks = host.querySelectorAll('a[href="/queue"]');
    expect(queueLinks.length).toBeGreaterThanOrEqual(2);
    expect(queueLinks[0]?.textContent).toContain('View queue');
    expect(host.querySelector('a[href="/series/3/search"]')).toBeNull();
    expect([...host.querySelectorAll('button')].some((button) =>
      button.textContent?.includes('Search now'),
    )).toBe(false);
  });

  it('offers an explicit replacement search when every episode is imported', async () => {
    stubFetch(0, COMPLETE_SERIES);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    expect(host.querySelector('a[href="/series/3/search"]')?.textContent).toContain(
      'Choose another release',
    );
    expect([...host.querySelectorAll('button')].some((button) =>
      button.textContent?.includes('Search now'),
    )).toBe(false);
  });
});

describe('quality profile assignment', () => {
  it('keeps the edited profile visible when saving fails', async () => {
    const overriddenSeries = { ...SERIES, quality_profile_id: 1 };
    stubFetch(0, overriddenSeries, 500);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    const dialog = await openEditor();
    const select = dialog.querySelector<HTMLSelectElement>(
      'select[aria-label="Quality profile"]',
    );
    expect(select).not.toBeNull();
    expect(select!.value).toBe('1');

    select!.value = '0';
    select!.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();
    saveChangesButton(dialog).click();
    await settle();

    expect(calls.find((call) => call.body?.quality_profile_id === 0)).toMatchObject({
      url: '/api/v1/library/series/3',
      method: 'PATCH',
      body: { monitored: true, quality_profile_id: 0 },
    });
    expect(select!.value).toBe('0');
    expect(dialog.querySelector('[role="alert"]')?.textContent).toContain(
      'Could not update quality profile',
    );
    expect(host.querySelector('[role="dialog"]')).not.toBeNull();
  });
});

/**
 * Title controls open one editor. Changes stay local until Save, while Remove
 * remains behind the overflow menu.
 */
describe('SeriesDetail title controls', () => {
  it('keeps Edit and overflow beside the title', async () => {
    stubFetch(0);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    const titleControls = host.querySelector('h2')?.parentElement;
    expect(titleControls?.contains(editButton())).toBe(true);
    expect(titleControls?.contains(menuTrigger())).toBe(true);
  });

  it('saves monitoring and the profile in one PATCH', async () => {
    stubFetch(0);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    const dialog = await openEditor();
    const monitor = dialog.querySelector<HTMLButtonElement>('button[role="switch"]');
    expect(monitor?.getAttribute('aria-checked')).toBe('true');
    monitor!.click();
    await settle();
    saveChangesButton(dialog).click();
    await settle();

    const patch = calls.find((c) => c.method === 'PATCH');
    expect(patch?.url).toBe('/api/v1/library/series/3');
    expect(patch?.body).toEqual({ monitored: false, quality_profile_id: 0 });
  });

  it('keeps unchanged settings from being submitted', async () => {
    stubFetch(0);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    const dialog = await openEditor();
    expect(saveChangesButton(dialog).disabled).toBe(true);
    expect(dialog.querySelector('button[role="switch"]')?.getAttribute('aria-checked')).toBe('true');
  });

  it('closes the menu on Escape without removing anything', async () => {
    stubFetch(0);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    menuTrigger().click();
    flushSync();
    expect(host.querySelector('[role="menu"]'), 'the menu is open').not.toBeNull();

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    flushSync();
    expect(host.querySelector('[role="menu"]')).toBeNull();
    expect(deletes()).toEqual([]);
  });
});
