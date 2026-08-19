import { describe, expect, it } from 'vitest';
import type { Job, SystemTask } from './api/types';
import type { DownloadStatus } from './api/types';
import {
  FOOTER_STACK_LIMIT,
  failedTaskCount,
  footerStack,
  isHousekeepingKind,
  isOneshotKind,
  jobKindLabel,
} from './tasks';

function task(extra: Partial<SystemTask> = {}): SystemTask {
  return {
    kind: 'rss_sync',
    name: 'RSS sync',
    description: 'Checks indexer feeds for newly posted releases.',
    interval_minutes: 15,
    last_run: '',
    last_result: 'ok',
    last_error: '',
    next_run: '',
    running: false,
    queued: true,
    ...extra,
  };
}

function job(extra: Partial<Job> = {}): Job {
  return {
    id: 1,
    kind: 'search_episode',
    payload: '',
    state: 'running',
    attempts: 1,
    run_after: '',
    lease_expires_at: '',
    last_error: '',
    created_at: '',
    updated_at: '',
    ...extra,
  };
}

function download(extra: Partial<DownloadStatus> = {}): DownloadStatus {
  return {
    id: 'hash-a',
    state: 'queued',
    name: 'Arrival.2016.1080p.BluRay-GROUP',
    progress: 0,
    bytes_done: 0,
    size: 1,
    down_rate: 0,
    up_rate: 0,
    eta_seconds: -1,
    ratio: 0,
    save_path: 'incomplete/arrival',
    error: '',
    created_at: '',
    updated_at: '',
    ...extra,
  };
}

describe('jobKindLabel', () => {
  it('names the kinds the product knows', () => {
    expect(jobKindLabel('search_episode')).toBe('Episode search');
    expect(jobKindLabel('sync_site')).toBe('Site catalogue');
  });

  it('falls back to spaces for an unknown kind', () => {
    expect(jobKindLabel('new_kind')).toBe('new kind');
  });
});

describe('kind sets', () => {
  it('keeps housekeeping off the footer', () => {
    expect(isHousekeepingKind('indexer_health')).toBe(true);
    expect(isHousekeepingKind('rss_sync')).toBe(false);
  });

  it('treats searches and imports as one-shots', () => {
    expect(isOneshotKind('search_movie')).toBe(true);
    expect(isOneshotKind('import')).toBe(true);
    expect(isOneshotKind('rss_sync')).toBe(false);
  });
});

describe('failedTaskCount', () => {
  it('counts only finished failures', () => {
    expect(failedTaskCount(null)).toBe(0);
    expect(
      failedTaskCount([
        task({ last_result: 'failed' }),
        task({ kind: 'backlog_sweep', last_result: 'ok' }),
        task({ kind: 'refresh_metadata', last_result: '' }),
      ]),
    ).toBe(1);
  });
});

describe('footerStack', () => {
  it('is silent when nothing is running and nothing failed', () => {
    expect(footerStack({ tasks: [task()], jobs: [] })).toEqual([]);
    expect(footerStack({ tasks: null, jobs: null })).toEqual([]);
  });

  it('names a grouped episode search and keeps a second show on its own row', () => {
    const rows = footerStack({
      tasks: [],
      jobs: [
        job({
          id: 1,
          kind: 'search_episode',
          subject: 'Transfixed',
          subject_kind: 'site',
          subject_id: 9,
        }),
        job({
          id: 2,
          kind: 'search_episode',
          subject: 'Transfixed',
          subject_kind: 'site',
          subject_id: 9,
        }),
        job({
          id: 3,
          kind: 'search_episode',
          subject: 'Severance',
          subject_kind: 'series',
          subject_id: 3,
          state: 'pending',
        }),
      ],
    });
    expect(rows.map((row) => ({ label: row.label, href: row.href }))).toEqual([
      { label: 'Searching 2 scenes from Transfixed', href: '/adult/sites/9' },
      { label: 'Searching Severance', href: '/series/3' },
    ]);
    expect(rows[0]?.stop).toEqual({
      kinds: ['search_episode'],
      subject_kind: 'site',
      subject_id: 9,
    });
  });

  it('names a movie search', () => {
    const rows = footerStack({
      tasks: [],
      jobs: [job({ kind: 'search_movie', subject: 'Arrival', subject_kind: 'movie', subject_id: 7 })],
    });
    expect(rows[0]).toMatchObject({
      label: 'Searching Arrival',
      href: '/movies/7',
      spinning: true,
    });
  });

  it('falls back to a count when a search has no title yet', () => {
    const rows = footerStack({
      tasks: [],
      jobs: [job({ id: 1, kind: 'search_movie' }), job({ id: 2, kind: 'search_episode' })],
    });
    expect(rows.map((row) => ({ label: row.label, href: row.href }))).toEqual([
      { label: 'Searching', href: '/wanted' },
      { label: 'Searching', href: '/wanted' },
    ]);
  });

  it('stacks a search, a queued download, and a conversion together', () => {
    const rows = footerStack({
      tasks: [],
      jobs: [job({ kind: 'search_movie', subject: 'Arrival', subject_kind: 'movie', subject_id: 7 })],
      downloads: [
        download(),
        download({ id: 'hash-b', name: 'Dune.2021.1080p', state: 'downloading' }),
      ],
      converting: 2,
    });
    expect(rows.map((row) => ({ label: row.label, href: row.href }))).toEqual([
      { label: 'Searching Arrival', href: '/movies/7' },
      { label: 'Waiting to download Arrival.2016.1080p.BluRay-GROUP', href: '/queue' },
      { label: 'Converting 2', href: '/convert' },
    ]);
  });

  it('counts several queued downloads as one row', () => {
    const rows = footerStack({
      tasks: [],
      jobs: [],
      downloads: [download(), download({ id: 'hash-b', name: 'Other' })],
    });
    expect(rows).toMatchObject([{ label: '2 downloads waiting', href: '/queue' }]);
  });

  it('ignores a download that has already started', () => {
    expect(
      footerStack({
        tasks: [],
        jobs: [],
        downloads: [download({ state: 'downloading' })],
      }),
    ).toEqual([]);
  });

  it('keeps imports, catalogues, and searches on the stack at once', () => {
    const rows = footerStack({
      tasks: [],
      jobs: [
        job({ id: 1, kind: 'import' }),
        job({ id: 2, kind: 'sync_site', subject: 'Transfixed', subject_kind: 'site', subject_id: 9 }),
        job({ id: 3, kind: 'search_movie', subject: 'Arrival', subject_kind: 'movie', subject_id: 7 }),
      ],
    });
    expect(rows.map((row) => ({ label: row.label, href: row.href }))).toEqual([
      { label: 'Searching Arrival', href: '/movies/7' },
      { label: 'Importing', href: '/history' },
      { label: 'Cataloguing Transfixed', href: '/adult/sites/9' },
    ]);
  });

  it('shows a library move after catalogues', () => {
    const moved = footerStack({
      tasks: [],
      jobs: [job({ kind: 'move_item' })],
    });
    expect(moved).toMatchObject([
      { label: 'Moving library items' },
    ]);
  });

  it('uses the task name for a single recurring run', () => {
    expect(footerStack({ tasks: [task({ running: true })], jobs: [] })).toMatchObject([
      {
        label: 'RSS sync',
        title: 'Checks indexer feeds for newly posted releases.',
        spinning: true,
        tone: 'accent',
      },
    ]);
  });

  it('summarises more than one recurring run', () => {
    expect(
      footerStack({
        tasks: [
          task({ running: true }),
          task({ kind: 'backlog_sweep', name: 'Backlog search', running: true }),
        ],
        jobs: [],
      }),
    ).toMatchObject([
      {
        label: '2 tasks running',
        title: 'RSS sync, Backlog search',
      },
    ]);
  });

  it('ignores housekeeping even when it is running', () => {
    expect(
      footerStack({
        tasks: [task({ kind: 'indexer_health', name: 'Indexer health', running: true })],
        jobs: [job({ kind: 'notification_dispatch', state: 'running' })],
      }),
    ).toEqual([]);
  });

  it('warns about a single failed last run when idle', () => {
    expect(
      footerStack({
        tasks: [task({ last_result: 'failed', last_error: 'indexer timed out' })],
        jobs: [],
      }),
    ).toMatchObject([
      {
        label: 'RSS sync failed',
        title: 'indexer timed out',
        spinning: false,
        tone: 'warning',
      },
    ]);
  });

  it('summarises more than one failed last run', () => {
    expect(
      footerStack({
        tasks: [
          task({ last_result: 'failed' }),
          task({ kind: 'backlog_sweep', name: 'Backlog search', last_result: 'failed' }),
        ],
        jobs: [],
      }),
    ).toMatchObject([
      {
        label: '2 tasks failed',
        title: 'RSS sync, Backlog search',
        tone: 'warning',
      },
    ]);
  });

  it('collapses a long stack behind a more row', () => {
    const jobs = Array.from({ length: FOOTER_STACK_LIMIT + 2 }, (_, index) =>
      job({
        id: index + 1,
        kind: 'search_movie',
        subject: `Movie ${index + 1}`,
        subject_kind: 'movie',
        subject_id: index + 1,
      }),
    );
    const rows = footerStack({ tasks: [], jobs });
    expect(rows).toHaveLength(FOOTER_STACK_LIMIT);
    expect(rows.at(-1)?.label).toBe('+3 more');
    expect(rows.at(-1)?.href).toBe('/movies/6');
  });
});
