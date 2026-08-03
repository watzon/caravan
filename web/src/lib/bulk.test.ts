/**
 * Bulk actions loop the per-item endpoints, so the two things that matter are
 * that one failure does not abandon the rest of the selection and that the
 * summary reports what actually happened.
 */
import { describe, expect, it } from 'vitest';
import { bulkSummary, runBulk } from './bulk';

describe('runBulk', () => {
  it('runs every id and reports the counts', async () => {
    const seen: number[] = [];
    const result = await runBulk([1, 2, 3], async (id) => {
      seen.push(id);
    });

    expect(seen).toEqual([1, 2, 3]);
    expect(result).toEqual({ ok: 3, failed: 0, total: 3 });
  });

  it('keeps going after a failure and counts it', async () => {
    const seen: number[] = [];
    const result = await runBulk([1, 2, 3], async (id) => {
      seen.push(id);
      if (id === 2) throw new Error('nope');
    });

    expect(seen, 'a failed item must not abandon the rest').toEqual([1, 2, 3]);
    expect(result).toEqual({ ok: 2, failed: 1, total: 3 });
  });

  it('does nothing for an empty selection', async () => {
    let called = 0;
    const result = await runBulk([], async () => {
      called++;
    });

    expect(called).toBe(0);
    expect(result).toEqual({ ok: 0, failed: 0, total: 0 });
  });
});

describe('bulkSummary', () => {
  it('names only the total when everything worked', () => {
    expect(bulkSummary({ ok: 5, failed: 0, total: 5 }, 'Monitored')).toBe('Monitored 5');
  });

  it('names the split when some failed', () => {
    expect(bulkSummary({ ok: 4, failed: 1, total: 5 }, 'Queued searches for')).toBe(
      'Queued searches for 4 of 5',
    );
  });
});
