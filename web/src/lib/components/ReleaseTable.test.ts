/**
 * The shared release table.
 *
 * It was lifted out of the per-item picker so the universal search renders the
 * same rows, which makes its contract worth pinning: the ranked order and the
 * "Best" marker come from release.ts, a flagged row stays grabbable, and the
 * grab button carries whichever verb its caller chose.
 */
import { afterEach, describe, expect, it } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import ReleaseTable from './ReleaseTable.svelte';
import type { ParsedRelease, Release } from '../api/types';

function parsed(overrides: Partial<ParsedRelease> = {}): ParsedRelease {
  return {
    title: 'Big Buck Bunny',
    year: 2008,
    season: 0,
    episodes: [],
    quality: '1080p',
    source: 'bluray',
    codec: 'x264',
    audio: 'AC3',
    bit_depth: 0,
    group: 'GROUP',
    proper: false,
    repack: false,
    edition: '',
    confidence: 0.9,
    ...overrides,
  };
}

function release(overrides: Partial<Release> = {}): Release {
  return {
    id: 1,
    indexer_id: 1,
    indexer: 'Test Indexer',
    title: 'Big.Buck.Bunny.2008.1080p.BluRay.x264-GROUP',
    guid: 'guid-1',
    download_url: 'magnet:?xt=urn:btih:abc',
    info_hash: 'abc',
    protocol: 'torrent',
    size: 4 * 1024 * 1024 * 1024,
    seeders: 20,
    leechers: 3,
    published_at: '2026-07-01T00:00:00Z',
    parsed: parsed(),
    compatibility: { verdict: 'unknown', reasons: [] },
    ...overrides,
  };
}

let host: HTMLElement | undefined;
let app: Record<string, unknown> | undefined;

function mountTable(props: Partial<{
  releases: Release[] | null;
  loading: boolean;
  busyGUID: string | null;
  grabLabel: string;
  ongrab: (release: Release) => void;
  emptyMessage: string;
  pageSize: number;
}> = {}): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  app = mount(ReleaseTable, {
    target: host,
    props: {
      releases: [],
      loading: false,
      busyGUID: null,
      ongrab: () => {},
      ...props,
    },
  }) as Record<string, unknown>;
  flushSync();
  return host;
}

afterEach(() => {
  if (app) unmount(app);
  host?.remove();
  app = undefined;
  host = undefined;
});

describe('ReleaseTable', () => {
  it('renders a skeleton instead of rows while a search is in flight', () => {
    mountTable({ releases: null, loading: true });
    expect(host!.querySelector('table')).toBeNull();
  });

  it('offers the indexer settings when a finished search found nothing', () => {
    mountTable({ releases: [] });
    expect(host!.textContent).toContain('No releases found');
    expect(host!.querySelector('a[href="/settings"]')).not.toBeNull();
  });

  it('lets a caller say why its own empty result is empty', () => {
    mountTable({ releases: [], emptyMessage: 'Nothing matched that query.' });
    expect(host!.textContent).toContain('Nothing matched that query.');
  });

  it('puts indexer, age and size under the name instead of in their own columns', () => {
    mountTable({
      releases: [release({ indexer: 'NZBgeek', published_at: '2026-08-12T00:00:00Z' })],
    });
    expect(host!.querySelector('th')?.textContent).toContain('Release');
    expect(host!.textContent).toContain('NZBgeek');
    expect(host!.textContent).not.toMatch(/\bPeers\b/);
  });

  it('keeps the full release title in the row for responsive overflow', () => {
    const title =
      'A.Very.Long.Release.Title.2026.2160p.WEB-DL.DDP5.1.DV.HDR.HEVC-RELEASEGROUP';
    mountTable({ releases: [release({ title })] });

    const cell = host!.querySelector<HTMLTableCellElement>('tbody td[title]');
    expect(cell?.textContent).toContain(title);
    expect(cell?.title).toBe(title);
  });

  it('gives leftover row width to the release name, not quality or score', () => {
    mountTable({ releases: [release()] });

    const headers = [...host!.querySelectorAll('thead th')].map((el) => el.className);
    expect(headers[0]).toContain('w-full');
    expect(headers[1]).toContain('w-[1%]');
    expect(headers[2]).toContain('w-[1%]');
    expect(headers[3]).toContain('w-[1%]');

    const cells = [...host!.querySelectorAll('tbody td')].map((el) => el.className);
    expect(cells[0]).toContain('w-full');
    expect(cells[0]).toContain('max-w-0');
    expect(cells[1]).toContain('w-[1%]');
    expect(cells[2]).toContain('w-[1%]');
    expect(cells[3]).toContain('w-[1%]');
  });

  it('pages long result sets, slicing only after the sort', () => {
    // Seeders descend with the index, so the sort keeps arrival order and the
    // first page provably holds the BEST rows, not the first-received ones.
    const many = Array.from({ length: 7 }, (_, i) =>
      release({ guid: `guid-${i}`, title: `Release ${i}`, seeders: 100 - i }),
    );
    // Arrive worst-first: the sort must pull the strongest rows into page one.
    mountTable({ releases: [...many].reverse(), pageSize: 3 });

    expect(host!.querySelectorAll('tbody tr').length).toBe(3);
    expect(host!.textContent).toContain('Release 0'); // 100 seeders — page one
    expect(host!.textContent).not.toContain('Release 6'); // 94 — behind the button
    expect(host!.textContent).toContain('Showing 3 of 7');

    const more = [...host!.querySelectorAll<HTMLButtonElement>('button')].find((b) =>
      b.textContent?.includes('Show 3 more'),
    )!;
    more.click();
    flushSync();
    expect(host!.querySelectorAll('tbody tr').length).toBe(6);
    expect(host!.textContent).toContain('Show 1 more');

    [...host!.querySelectorAll<HTMLButtonElement>('button')]
      .find((b) => b.textContent?.includes('Show 1 more'))!
      .click();
    flushSync();
    expect(host!.querySelectorAll('tbody tr').length).toBe(7);
    // Everything is on screen, so the footer withdraws.
    expect(host!.textContent).not.toContain('Showing');
  });

  it('marks the top-ranked row as best, whatever order it arrived in', () => {
    mountTable({
      releases: [
        release({ guid: 'sd', parsed: parsed({ quality: '480p' }) }),
        release({ guid: 'uhd', parsed: parsed({ quality: '2160p' }) }),
      ],
    });
    const first = host!.querySelector('tbody tr');
    expect(first?.textContent).toContain('Best');
    expect(host!.querySelectorAll('tbody tr')).toHaveLength(2);
  });

  it('keeps a flagged release grabbable rather than deciding for the user', () => {
    // Zero seeders on a torrent is the flag the picker de-emphasizes.
    mountTable({ releases: [release({ seeders: 0, leechers: 0 })] });
    const button = host!.querySelector<HTMLButtonElement>('tbody button');
    expect(button?.disabled).toBe(false);
  });

  it('carries the caller’s grab verb and reports the busy row', () => {
    mountTable({
      releases: [release({ guid: 'guid-1' })],
      grabLabel: 'Grab into…',
      busyGUID: 'guid-1',
    });
    // The busy row says Grabbing…; every other row keeps the verb.
    expect(host!.textContent).toContain('Grabbing…');
    expect(host!.textContent).not.toContain('Grab into…');
  });

  it('replaces Grab with Downloading when that release is already in flight', () => {
    mountTable({ releases: [release({ queue_state: 'downloading' })] });
    expect(host!.textContent).toContain('Downloading');
    expect(host!.querySelector('tbody button')).toBeNull();
  });

  it('keeps a quiet Grab again on an already imported release', () => {
    mountTable({ releases: [release({ queue_state: 'downloaded' })] });
    expect(host!.textContent).toContain('Downloaded');
    expect(host!.querySelector('tbody button')?.textContent).toContain('Grab again');
  });

  it('hands the clicked release back to its caller', () => {
    let grabbed: Release | null = null;
    mountTable({
      releases: [release({ guid: 'guid-1' })],
      ongrab: (r) => (grabbed = r),
    });
    host!.querySelector<HTMLButtonElement>('tbody button')!.click();
    flushSync();
    expect(grabbed).not.toBeNull();
    expect((grabbed as unknown as Release).guid).toBe('guid-1');
  });
});
