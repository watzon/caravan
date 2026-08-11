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

  it('describes a container-only conversion without exposing remux terminology', () => {
    const badge = compatBadge({
      verdict: 'needs-remux',
      reasons: ['MKV container (profile allows MP4/M4V)'],
    });
    expect(badge?.key).toBe('tv-remux');
    expect(badge?.tone).toBe('warning');
    expect(badge?.label).toBe('NEEDS CONVERT');
    expect(badge?.title).toBe(
      'The streams are compatible, but the container is not. Caravan can convert it without re-encoding. MKV container (profile allows MP4/M4V).',
    );
    expect(`${badge?.label} ${badge?.title}`).not.toMatch(/remux/i);
  });

  it('renders without reasons rather than printing an empty tail', () => {
    const badge = compatBadge({ verdict: 'incompatible', reasons: [] });
    expect(badge?.title).not.toMatch(/\s{2,}/);
    expect(badge?.title).toBe(
      'This file will not play natively with the selected TV profile. Caravan must re-encode the video or audio.',
    );
  });
});
