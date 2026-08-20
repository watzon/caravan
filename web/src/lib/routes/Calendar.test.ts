import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Calendar from './Calendar.svelte';
import TopBar from '../layout/TopBar.svelte';
import type { SessionLibrary } from '../api/types';
import { session } from '../state/session.svelte';

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
}

function todayISO() {
  const date = new Date();
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
}


function tomorrowISO() {
  const date = new Date();
  date.setDate(date.getDate() + 1);
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
}
let host: HTMLElement;
let app: Record<string, unknown>;
let topbar: Record<string, unknown> | undefined;

beforeEach(() => {
  const date = todayISO();
  const tomorrow = tomorrowISO();
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    if (String(input).includes('/calendar?')) {
      return jsonResponse({ entries: [
        { kind: 'movie', date, title: 'On Disk', movie_id: 1, monitored: true, has_file: true, status: 'downloaded', library_id: 1 },
        { kind: 'movie', date, title: 'In Progress', movie_id: 2, monitored: true, has_file: false, status: 'downloading', library_id: 1 },
        { kind: 'movie', date, title: 'Still Missing', movie_id: 3, monitored: true, has_file: false, status: 'missing', library_id: 1 },
        { kind: 'episode', date: tomorrow, title: 'Future', series_id: 4, episode_id: 40, season_number: 1, episode_number: 2, episode_title: 'Future', monitored: true, has_file: false, status: 'unaired', library_id: 2 },
        { kind: 'episode', date: tomorrow, title: 'Chainsmoker Cat', series_id: 5, episode_id: 56, season_number: 1, episode_number: 6, episode_title: 'Episode 6', monitored: true, has_file: false, status: 'unaired', library_id: 2 },
        { kind: 'episode', date: tomorrow, title: 'Series Name', series_id: 6, episode_id: 64, episode_number: 4, episode_title: 'Episode 4', monitored: true, has_file: false, status: 'unaired', library_id: 2 },
        { kind: 'episode', date, title: 'Vixen', series_id: 9, series_kind: 'adult', episode_id: 24, season_number: 2026, episode_number: 1, episode_title: 'A Scene', monitored: true, has_file: false, status: 'missing', library_id: 9 },
      ] });
    }
    throw new Error(`unexpected fetch: ${String(input)}`);
  }));
  vi.useFakeTimers();
  host = document.createElement('div');
  document.body.appendChild(host);
  topbar = undefined;
});

afterEach(() => {
  unmount(app);
  if (topbar) unmount(topbar);
  host.remove();
  session.user = null;
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

async function settle() {
  await vi.advanceTimersByTimeAsync(0);
  await Promise.resolve();
  flushSync();
}

describe('Calendar', () => {
  it('populates the month grid and colors every calendar status', async () => {
    app = mount(Calendar, { target: host });
    await settle();

    expect(host.querySelectorAll('.min-h-28')).toHaveLength(42);
    const expectations: Array<[string, string]> = [
      ['On Disk', 'bg-success-tint'],
      ['In Progress', 'bg-info-tint'],
      ['Still Missing', 'bg-danger-tint'],
      ['S01E02 Future', 'bg-raised'],
    ];
    for (const [title, expectedClass] of expectations) {
      const entry = host.querySelector(`[title="${title}"]`);
      expect(entry, `${title} calendar entry`).not.toBeNull();
      expect(entry?.className).toContain(expectedClass);
    }

    expect(host.querySelector('[title="Chainsmoker Cat S01E06"]')).not.toBeNull();
  });

  it('contains the wide month grid inside a shrinkable horizontal scroller', async () => {
    app = mount(Calendar, { target: host });
    await settle();

    const month = host.querySelector<HTMLElement>('[aria-label="Month calendar"]');
    expect(month?.classList.contains('min-w-0')).toBe(true);
    expect(month?.classList.contains('max-w-full')).toBe(true);
    expect(month?.firstElementChild?.classList.contains('min-w-[760px]')).toBe(true);
    expect(month?.parentElement?.classList.contains('w-full')).toBe(true);
    expect(month?.parentElement?.classList.contains('min-w-0')).toBe(true);
  });

  it('names month controls and shows and announces status in each day chip', async () => {
    app = mount(Calendar, { target: host });
    await settle();

    expect(host.querySelector('button[aria-label="Previous month"]')).not.toBeNull();
    expect(host.querySelector('button[aria-label="Next month"]')).not.toBeNull();

    const expectations: Array<[string, string, string]> = [
      ['On Disk', 'On disk', 'On Disk, On disk'],
      ['In Progress', 'Downloading', 'In Progress, Downloading'],
      ['Still Missing', 'Missing', 'Still Missing, Missing'],
      ['S01E02 Future', 'Not yet released', 'Future S01E02 Future, Not yet released'],
      ['Chainsmoker Cat S01E06', 'Not yet released', 'Chainsmoker Cat S01E06, Not yet released'],
    ];
    for (const [title, visibleStatus, accessibleName] of expectations) {
      const entry = host.querySelector(`[title="${title}"]`);
      expect(entry?.querySelector('span:last-child')?.textContent).toBe(visibleStatus);
      expect(entry?.getAttribute('aria-label')).toBe(accessibleName);
    }
  });

  it('renders a generic season-zero episode once in Month and Agenda when the wire omits its season number', async () => {
    app = mount(Calendar, { target: host });
    await settle();
    topbar = mount(TopBar, { target: host, props: { title: 'Calendar' } });
    flushSync();

    const month = host.querySelector('[aria-label="Month calendar"]');
    const monthEntries = month?.querySelectorAll('[title="Series Name S00E04"]');
    expect(monthEntries).toHaveLength(1);
    expect(monthEntries?.[0]?.querySelector('span')?.textContent).toBe('Series Name S00E04');

    const agendaButton = Array.from(host.querySelectorAll('button')).find((button) => button.textContent === 'Agenda');
    expect(agendaButton).toBeDefined();
    agendaButton?.click();
    flushSync();

    const agenda = host.querySelector('[aria-label="Calendar agenda"]');
    expect(agenda?.querySelector('a[href="/series/5#s1e6"]')?.textContent).toBe('Chainsmoker Cat S01E06');
    expect(agenda?.querySelector('a[href="/series/5#s1e6"]')?.getAttribute('title')).toBe('Chainsmoker Cat S01E06');
    const specialEntries = Array.from(agenda?.querySelectorAll('a[href="/series/6#s0e4"]') ?? []);
    expect(specialEntries).toHaveLength(1);
    expect(specialEntries[0]?.textContent).toBe('Series Name S00E04');
    expect(specialEntries[0]?.getAttribute('title')).toBe('Series Name S00E04');
  });

  it('marks today without a focus-style ring', async () => {
    app = mount(Calendar, { target: host });
    await settle();

    const today = host.querySelector('[data-today="true"]');
    expect(today).not.toBeNull();
    expect(today?.className).toContain('bg-accent-tint');
    expect(today?.querySelector('[aria-current="date"]')?.textContent).toContain('Today');
    expect(today?.querySelector('[aria-current="date"]')?.className).toContain('relative');
    expect(today?.className).not.toContain('ring-');
    expect(today?.querySelector('p')?.className).toContain('rounded-full');
  });

  it('links movies, episodes and adult scenes to their library pages', async () => {
    app = mount(Calendar, { target: host });
    await settle();
    topbar = mount(TopBar, { target: host, props: { title: 'Calendar' } });
    flushSync();

    expect(host.querySelector('a[href="/movies/1"]')).not.toBeNull();
    expect(host.querySelector('a[href="/series/4#s1e2"]')).not.toBeNull();

    const agendaButton = Array.from(host.querySelectorAll('button')).find((button) => button.textContent === 'Agenda');
    expect(agendaButton).toBeDefined();
    agendaButton!.click();
    flushSync();

    expect(host.querySelector('a[href="/movies/2"]')).not.toBeNull();
    expect(host.querySelector('a[href="/series/5#s1e6"]')).not.toBeNull();
    expect(host.querySelector('a[href="/adult/sites/9#y2026n1"]')?.textContent).toContain('Vixen');
  });

  it('keeps only the libraries that are checked on both month and agenda', async () => {
    session.user = {
      username: 'root',
      role: 'admin',
      open: false,
      adult: true,
      libraries: [
        { id: 1, kind: 'movie', name: 'Movies', icon: '', slug: 'movies' } satisfies SessionLibrary,
        { id: 2, kind: 'tv', name: 'Series', icon: '', slug: 'series' } satisfies SessionLibrary,
        { id: 9, kind: 'adult', name: 'Adult', icon: '', slug: 'adult' } satisfies SessionLibrary,
      ],
    };
    app = mount(Calendar, { target: host });
    await settle();
    topbar = mount(TopBar, { target: host, props: { title: 'Calendar' } });
    flushSync();

    const agendaButton = Array.from(host.querySelectorAll('button')).find((button) => button.textContent === 'Agenda');
    agendaButton!.click();
    flushSync();

    expect(host.textContent).toContain('All libraries');
    expect(host.querySelector('a[href="/movies/1"]')).not.toBeNull();
    expect(host.querySelector('a[href="/series/4#s1e2"]')).not.toBeNull();
    expect(host.querySelector('a[href="/adult/sites/9#y2026n1"]')).not.toBeNull();

    const trigger = [...host.querySelectorAll<HTMLButtonElement>('button[aria-haspopup="dialog"]')].find((button) =>
      button.textContent?.includes('All libraries'),
    );
    trigger!.click();
    flushSync();
    const options = [...host.querySelectorAll<HTMLButtonElement>('[role="dialog"] li button')];
    options[0]!.click();
    flushSync();

    expect(host.querySelector('a[href="/movies/1"]')).not.toBeNull();
    expect(host.querySelector('a[href="/series/4#s1e2"]')).toBeNull();
    expect(host.querySelector('a[href="/adult/sites/9#y2026n1"]')).toBeNull();

    options[1]!.click();
    flushSync();
    expect(host.querySelector('a[href="/movies/1"]')).not.toBeNull();
    expect(host.querySelector('a[href="/series/4#s1e2"]')).not.toBeNull();
    expect(host.querySelector('a[href="/adult/sites/9#y2026n1"]')).toBeNull();

    const monthButton = Array.from(host.querySelectorAll('button')).find((button) => button.textContent === 'Month');
    monthButton!.click();
    flushSync();
    expect(host.querySelector('[title="On Disk"]')).not.toBeNull();
    expect(host.querySelector('[title="S01E02 Future"]')).not.toBeNull();
    expect(host.querySelector('a[href="/adult/sites/9#y2026n1"]')).toBeNull();
  });
});
