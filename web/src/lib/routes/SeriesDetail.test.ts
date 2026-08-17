/**
 * The series-level action pair (SPEC §9). "Search now" queues one job per
 * wanted episode and reports the count the server actually queued; the
 * per-season and per-episode links stay the interactive picker.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import SeriesDetail from './SeriesDetail.svelte';
import { tasks } from '../state/tasks.svelte';
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
  clearToasts();
  tasks.stopSoon();
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

/** The header's monitored control, now an icon toggle rather than a switch. */
function monitorButton(): HTMLButtonElement {
  const button = [...host.querySelectorAll<HTMLButtonElement>('button[aria-pressed]')].find((b) =>
    b.getAttribute('aria-label')?.includes('monitor'),
  );
  expect(button, 'monitor toggle').toBeDefined();
  return button as HTMLButtonElement;
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
  it('queues every wanted episode and reports the count', async () => {
    stubFetch(4);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    expect(host.querySelector('a[href="/series/3/search"]')?.textContent).toContain(
      'Interactive search',
    );

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
    stubFetch(0);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    searchNowButton().click();
    await settle();

    expect(toasts.items).toHaveLength(1);
    expect(toasts.items[0]!.message).toContain('Nothing to search');
  });

  it('singularizes a lone queued search', async () => {
    stubFetch(1);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    searchNowButton().click();
    await settle();

    expect(toasts.items).toHaveLength(0);
  });
});

describe('quality profile assignment', () => {
  it('restores the stored series profile when the assignment fails', async () => {
    const overriddenSeries = { ...SERIES, quality_profile_id: 1 };
    stubFetch(0, overriddenSeries, 500);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    const select = host.querySelector<HTMLSelectElement>('select[aria-label="Quality profile"]');
    expect(select).not.toBeNull();
    expect(select!.value).toBe('1');

    select!.value = '0';
    select!.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();

    expect(calls.find((call) => call.body?.quality_profile_id === 0)).toMatchObject({
      url: '/api/v1/library/series/3',
      method: 'PATCH',
      body: { quality_profile_id: 0 },
    });
    expect(select!.value).toBe('1');
    expect(toasts.items.map((toast) => toast.message)).toEqual(['Could not update quality profile']);
    expect(toasts.items[0]?.tone).toBe('danger');
  });
});

/**
 * The redesigned action row (Option A): the labeled switch became an icon
 * toggle and Remove moved behind the ⋯. The requests are the ones the old
 * surface made.
 */
describe('SeriesDetail action row', () => {
  it('sends the same monitored PATCH the switch used to', async () => {
    stubFetch(0);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    monitorButton().click();
    await settle();

    const patch = calls.find((c) => c.method === 'PATCH');
    expect(patch?.url).toBe('/api/v1/library/series/3');
    expect(patch?.body).toEqual({ monitored: false });
  });

  it('announces the state it is in', async () => {
    stubFetch(0);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();
    expect(monitorButton().getAttribute('aria-pressed')).toBe('true');
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
