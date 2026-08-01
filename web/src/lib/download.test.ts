import { describe, expect, it } from 'vitest';
import type { DownloadState, DownloadStatus } from './api/types';
import {
  DEFAULT_ENGINE,
  countActiveDownloads,
  downloadStateMeta,
  engineLabel,
  isActiveDownload,
  sortDownloads,
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
    expect(engineLabel(download())).toBe(DEFAULT_ENGINE);
  });

  it('uses what the server said when it says anything', () => {
    expect(engineLabel(download({ engine: 'qbittorrent' }))).toBe('qbittorrent');
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
