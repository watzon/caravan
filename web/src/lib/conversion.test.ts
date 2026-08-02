import { describe, expect, it } from 'vitest';
import type { Conversion, TVCompatibility } from './api/types';
import {
  activeConversions,
  conversionStateMeta,
  convertible,
  openConversionFor,
  strategyLabel,
} from './conversion';

function row(overrides: Partial<Conversion>): Conversion {
  return {
    id: 1,
    media_file_id: 1,
    source_path: 'library/Movies/A (2001)/A (2001).mkv',
    output_path: '',
    strategy: '',
    profile_id: 'safe',
    status: 'queued',
    error: '',
    created_at: '2026-08-01T00:00:00Z',
    updated_at: '2026-08-01T00:00:00Z',
    ...overrides,
  };
}

describe('conversionStateMeta', () => {
  it('maps every status onto the shared status palette', () => {
    expect(conversionStateMeta('queued')).toMatchObject({ tone: 'neutral', active: true });
    expect(conversionStateMeta('running')).toMatchObject({ tone: 'accent', active: true });
    expect(conversionStateMeta('done')).toMatchObject({ tone: 'success', active: false });
    expect(conversionStateMeta('failed')).toMatchObject({ tone: 'danger', active: false });
    expect(conversionStateMeta('cancelled')).toMatchObject({ tone: 'neutral', active: false });
  });

  it('renders a status the server invents later without crashing', () => {
    expect(conversionStateMeta('paused-for-lunch')).toMatchObject({
      label: 'paused-for-lunch',
      tone: 'neutral',
      active: false,
    });
  });
});

describe('strategyLabel', () => {
  it('says what the strategy costs, not just what it is called', () => {
    expect(strategyLabel('remux')).toContain('stream copy');
    expect(strategyLabel('transcode')).toContain('re-encode');
    expect(strategyLabel('none')).toBe('Nothing to do');
  });

  it('reads as undecided before the file has been probed', () => {
    expect(strategyLabel('')).toBe('Deciding…');
  });
});

describe('convertible', () => {
  const compat = (verdict: TVCompatibility['verdict']): TVCompatibility => ({
    verdict,
    reasons: [],
  });

  it('offers a conversion only where there is something to fix', () => {
    expect(convertible(compat('needs-remux'))).toBe(true);
    expect(convertible(compat('incompatible'))).toBe(true);
  });

  it('never offers one on a guess', () => {
    // "unknown" is the important case: re-encoding a file nothing could be
    // judged about destroys quality for nothing.
    expect(convertible(compat('unknown'))).toBe(false);
    expect(convertible(compat('compatible'))).toBe(false);
    expect(convertible(null)).toBe(false);
    expect(convertible(undefined)).toBe(false);
  });
});

describe('queue helpers', () => {
  const rows = [
    row({ id: 1, media_file_id: 10, status: 'queued' }),
    row({ id: 2, media_file_id: 11, status: 'running' }),
    row({ id: 3, media_file_id: 12, status: 'done' }),
    row({ id: 4, media_file_id: 13, status: 'failed' }),
  ];

  it('counts only what the queue will still act on', () => {
    expect(activeConversions(rows).map((r) => r.id)).toEqual([1, 2]);
  });

  it('finds a file’s open conversion and ignores its finished ones', () => {
    expect(openConversionFor(rows, 11)?.id).toBe(2);
    expect(openConversionFor(rows, 12)).toBeNull();
    expect(openConversionFor(rows, 99)).toBeNull();
  });
});
