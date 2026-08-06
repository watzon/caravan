import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Calendar from './Calendar.svelte';

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

beforeEach(() => {
  const date = todayISO();
  const tomorrow = tomorrowISO();
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    if (String(input).includes('/calendar?')) {
      return jsonResponse({ entries: [
        { kind: 'movie', date, title: 'On Disk', movie_id: 1, monitored: true, has_file: true, status: 'downloaded' },
        { kind: 'movie', date, title: 'In Progress', movie_id: 2, monitored: true, has_file: false, status: 'downloading' },
        { kind: 'movie', date, title: 'Still Missing', movie_id: 3, monitored: true, has_file: false, status: 'missing' },
        { kind: 'episode', date: tomorrow, title: 'Future', series_id: 4, season_number: 1, episode_number: 2, episode_title: 'Future', monitored: true, has_file: false, status: 'unaired' },
        { kind: 'episode', date: tomorrow, title: 'Chainsmoker Cat', series_id: 5, season_number: 1, episode_number: 6, episode_title: 'Episode 6', monitored: true, has_file: false, status: 'unaired' },
      ] });
    }
    throw new Error(`unexpected fetch: ${String(input)}`);
  }));
  vi.useFakeTimers();
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  unmount(app);
  host.remove();
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

  it('names month controls and includes status in each day chip', async () => {
    app = mount(Calendar, { target: host });
    await settle();

    expect(host.querySelector('button[aria-label="Previous month"]')).not.toBeNull();
    expect(host.querySelector('button[aria-label="Next month"]')).not.toBeNull();
    expect(host.querySelector('[title="On Disk"]')?.getAttribute('aria-label')).toBe('On Disk, On disk');
    expect(host.querySelector('[title="S01E02 Future"]')?.getAttribute('aria-label')).toBe('Future S01E02 Future, Not yet released');
  });

  it('marks today without a focus-style ring', async () => {
    app = mount(Calendar, { target: host });
    await settle();

    const today = host.querySelector('[data-today="true"]');
    expect(today).not.toBeNull();
    expect(today?.className).toContain('bg-accent-tint');
    expect(today?.className).not.toContain('ring-');
    expect(today?.querySelector('p')?.className).toContain('rounded-full');
  });
});
