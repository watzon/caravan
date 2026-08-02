import { describe, expect, it } from 'vitest';
import { compatBadge } from './tvcompat';

describe('compatBadge', () => {
  it('says nothing when the verdict is compatible or unknown', () => {
    expect(compatBadge({ verdict: 'compatible', reasons: [] })).toBeNull();
    expect(compatBadge({ verdict: 'unknown', reasons: [] })).toBeNull();
    expect(compatBadge(undefined)).toBeNull();
    expect(compatBadge(null)).toBeNull();
  });

  it('warns about a re-encode and carries every reason in the tooltip', () => {
    const badge = compatBadge({
      verdict: 'incompatible',
      reasons: ['HEVC video (profile allows H.264)', 'DTS audio (profile allows AAC)'],
    });
    expect(badge?.label).toBe('NEEDS CONVERT');
    expect(badge?.tone).toBe('warning');
    expect(badge?.title).toContain('HEVC video (profile allows H.264)');
    expect(badge?.title).toContain('DTS audio (profile allows AAC)');
  });

  it('separates a container-only problem from a stream problem', () => {
    const badge = compatBadge({
      verdict: 'needs-remux',
      reasons: ['MKV container (profile allows MP4/M4V)'],
    });
    expect(badge?.label).toBe('NEEDS REMUX');
    expect(badge?.title).toContain('remux');
    expect(badge?.title).toContain('MKV container');
  });

  it('renders without reasons rather than printing an empty tail', () => {
    const badge = compatBadge({ verdict: 'incompatible', reasons: [] });
    expect(badge?.title).not.toMatch(/\s{2,}/);
    expect(badge?.title.endsWith('re-encoded.')).toBe(true);
  });
});
