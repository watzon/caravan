import { describe, expect, it } from 'vitest';
import type { DownloadState, DownloadStatus, UnhealthyDownloadClient } from './api/types';
import {
  DEFAULT_ENGINE,
  countActiveDownloads,
  downloadPhaseLabel,
  downloadStateMeta,
  engineLabel,
  isActiveDownload,
  isFinishedDownload,
  sortDownloads,
  unreachableClientBanner,
} from './download';

function download(overrides: Partial<DownloadStatus> = {}): DownloadStatus {
  return {
    id: 'abc',
    state: 'downloading',
    name: 'Big.Buck.Bunny.2008.1080p.BluRay.x264-GROUP',
    progress: 0.42,
    bytes_done: 1024,
    size: 2048,
    down_rate: 1000,
    up_rate: 0,
    eta_seconds: 60,
    ratio: 0,
    save_path: 'incomplete/big-buck-bunny',
    error: '',
    ...overrides,
  };
}

describe('downloadStateMeta', () => {
  const states: DownloadState[] = [
    'queued',
    'downloading',
    'seeding',
    'completed',
    'failed',
    'paused',
  ];

  for (const state of states) {
    it(`labels ${state}`, () => {
      expect(downloadStateMeta(state).label).not.toBe('');
    });
  }

  it('renders a state the server invents later without crashing', () => {
    const meta = downloadStateMeta('checking');
    expect(meta.label).toBe('checking');
    expect(meta.tone).toBe('neutral');
    expect(meta.active).toBe(false);
  });
});

describe('isActiveDownload', () => {
  it('counts work the engine is still doing', () => {
    expect(isActiveDownload(download({ state: 'downloading' }))).toBe(true);
    expect(isActiveDownload(download({ state: 'queued' }))).toBe(true);
    expect(isActiveDownload(download({ state: 'seeding' }))).toBe(true);
  });

  it('excludes states that are waiting on the user or finished', () => {
    // A paused download waits on a human, so a badge counting it never clears.
    expect(isActiveDownload(download({ state: 'paused' }))).toBe(false);
    expect(isActiveDownload(download({ state: 'completed' }))).toBe(false);
    expect(isActiveDownload(download({ state: 'failed' }))).toBe(false);
  });
});

describe('isFinishedDownload', () => {
  it('treats completed and paused-after-finishing as done', () => {
    expect(isFinishedDownload(download({ state: 'completed', progress: 1 }))).toBe(true);
    // A torrent that finished its download and sits parked instead of seeding.
    expect(isFinishedDownload(download({ state: 'paused', progress: 1 }))).toBe(true);
  });

  it('keeps everything that still needs the engine or the user visible', () => {
    expect(isFinishedDownload(download({ state: 'paused', progress: 0.4 }))).toBe(false);
    expect(isFinishedDownload(download({ state: 'seeding', progress: 1 }))).toBe(false);
    expect(isFinishedDownload(download({ state: 'downloading', progress: 0.9 }))).toBe(false);
    expect(isFinishedDownload(download({ state: 'failed', progress: 1 }))).toBe(false);
    expect(isFinishedDownload(download({ state: 'queued', progress: 0 }))).toBe(false);
  });
});

describe('countActiveDownloads', () => {
  it('counts only the active ones', () => {
    expect(
      countActiveDownloads([
        download({ id: '1', state: 'downloading' }),
        download({ id: '2', state: 'paused' }),
        download({ id: '3', state: 'seeding' }),
        download({ id: '4', state: 'completed' }),
      ]),
    ).toBe(2);
  });

  it('is zero for an empty queue', () => {
    expect(countActiveDownloads([])).toBe(0);
  });
});

describe('engineLabel', () => {
  it('falls back to the embedded engine when the server omits it', () => {
    expect(engineLabel(download())).toBe('Embedded');
    expect(DEFAULT_ENGINE).toBe('embedded');
  });

  // The row says which client holds the download, spelled the way the settings
  // screen spells it rather than the way the database stores it.
  it('names the download client that holds it', () => {
    expect(engineLabel(download({ engine: 'qbittorrent' }))).toBe('qBittorrent');
    expect(engineLabel(download({ engine: 'sabnzbd' }))).toBe('SABnzbd');
    expect(engineLabel(download({ engine: 'nzbget' }))).toBe('NZBGet');
  });

  it('shows a backend it does not know rather than nothing', () => {
    expect(engineLabel(download({ engine: 'transmission' }))).toBe('transmission');
  });
});

describe('unreachableClientBanner', () => {
  const client = (over: Partial<UnhealthyDownloadClient> = {}): UnhealthyDownloadClient => ({
    id: 1,
    name: 'Seedbox',
    type: 'qbittorrent',
    error: 'connection refused',
    since: '2026-08-01T09:30:00Z',
    ...over,
  });

  it('is silent when every client is answering', () => {
    expect(unreachableClientBanner(undefined)).toBeNull();
    expect(unreachableClientBanner([])).toBeNull();
  });

  // The user has to be told which client, why, and that the rest of the queue
  // is still running — a bare "unreachable" reads as "Caravan is broken".
  it('names the client, the reason, and what is unaffected', () => {
    const banner = unreachableClientBanner([client()]);
    expect(banner?.title).toBe('Download client Seedbox is unreachable');
    expect(banner?.message).toContain('connection refused');
    expect(banner?.message).toContain('unaffected');
  });

  it('names every client that is down, and each distinct reason once', () => {
    const banner = unreachableClientBanner([
      client(),
      client({ id: 2, name: 'Usenet box', type: 'sabnzbd' }),
    ]);
    expect(banner?.title).toBe('Download clients Seedbox, Usenet box are unreachable');
    expect(banner?.message.match(/connection refused/g)).toHaveLength(1);
  });
});

describe('sortDownloads', () => {
  it('puts live work first and finished work last', () => {
    const sorted = sortDownloads([
      download({ id: '1', state: 'completed', name: 'a' }),
      download({ id: '2', state: 'failed', name: 'b' }),
      download({ id: '3', state: 'downloading', name: 'c' }),
      download({ id: '4', state: 'queued', name: 'd' }),
    ]);
    expect(sorted.map((d) => d.state)).toEqual([
      'downloading',
      'queued',
      'failed',
      'completed',
    ]);
  });

  it('orders within a state by name, so rows do not jump between polls', () => {
    const sorted = sortDownloads([
      download({ id: '1', state: 'downloading', name: 'Zebra' }),
      download({ id: '2', state: 'downloading', name: 'Aardvark' }),
    ]);
    expect(sorted.map((d) => d.name)).toEqual(['Aardvark', 'Zebra']);
  });

  it('does not mutate its input', () => {
    const input = [
      download({ id: '1', state: 'completed' }),
      download({ id: '2', state: 'downloading' }),
    ];
    sortDownloads(input);
    expect(input.map((d) => d.id)).toEqual(['1', '2']);
  });
});

describe('downloadPhaseLabel', () => {
  it('names the stages the state badge cannot express', () => {
    expect(downloadPhaseLabel(download({ phase: 'repairing' }))).toBe('Repairing');
    expect(downloadPhaseLabel(download({ phase: 'extracting' }))).toBe('Extracting');
  });

  it('says nothing while the download is merely downloading', () => {
    // The state badge beside it already reads "Downloading"; a second badge
    // saying the same thing is noise.
    expect(downloadPhaseLabel(download({ phase: 'downloading' }))).toBe('');
  });

  it('says nothing for an engine that has no phases', () => {
    expect(downloadPhaseLabel(download())).toBe('');
    expect(downloadPhaseLabel(download({ phase: '' }))).toBe('');
  });

  it('shows a phase the server invents later rather than dropping it', () => {
    const future = download({ phase: 'verifying' as never });
    expect(downloadPhaseLabel(future)).toBe('Verifying');
  });
});
