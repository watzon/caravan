import { describe, expect, it } from 'vitest';
import type { ParsedRelease, Release } from './api/types';
import { isFlagged, releaseFlags, releaseScore, sortReleases } from './release';

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

describe('releaseScore', () => {
  it('ranks a better quality above a worse one, all else equal', () => {
    const uhd = release({ parsed: parsed({ quality: '2160p' }) });
    const hd = release({ parsed: parsed({ quality: '1080p' }) });
    const sd = release({ parsed: parsed({ quality: '480p' }) });
    expect(releaseScore(uhd)).toBeGreaterThan(releaseScore(hd));
    expect(releaseScore(hd)).toBeGreaterThan(releaseScore(sd));
  });

  it('ranks an unknown quality below every known rung', () => {
    const known = release({ parsed: parsed({ quality: '480p' }) });
    const unknown = release({ parsed: parsed({ quality: 'unknown' }) });
    expect(releaseScore(known)).toBeGreaterThan(releaseScore(unknown));
  });

  it('breaks a quality tie on source', () => {
    const bluray = release({ parsed: parsed({ source: 'bluray' }) });
    const hdtv = release({ parsed: parsed({ source: 'hdtv' }) });
    expect(releaseScore(bluray)).toBeGreaterThan(releaseScore(hdtv));
  });

  it('rewards a healthier swarm', () => {
    const many = release({ seeders: 200 });
    const few = release({ seeders: 5 });
    expect(releaseScore(many)).toBeGreaterThan(releaseScore(few));
  });

  it('ignores seeders for Usenet, which has no swarm', () => {
    const a = release({ protocol: 'usenet', seeders: 0, leechers: 0 });
    const b = release({ protocol: 'usenet', seeders: 999, leechers: 0 });
    expect(releaseScore(a)).toBe(releaseScore(b));
  });

  it('rewards a PROPER', () => {
    expect(releaseScore(release({ parsed: parsed({ proper: true }) }))).toBeGreaterThan(
      releaseScore(release()),
    );
  });

  it('sinks a flagged 2160p below a clean 480p', () => {
    // The whole point of the penalty: a CAM claiming 4K is still a CAM.
    const cam = release({
      parsed: parsed({ quality: '2160p', source: 'cam' }),
    });
    const clean = release({ parsed: parsed({ quality: '480p', source: 'dvd' }) });
    expect(releaseScore(clean)).toBeGreaterThan(releaseScore(cam));
  });
});

describe('releaseFlags', () => {
  it('finds nothing wrong with an ordinary release', () => {
    expect(releaseFlags(release())).toEqual([]);
    expect(isFlagged(release())).toBe(false);
  });

  it('flags a cinema recording as dangerous', () => {
    const flags = releaseFlags(release({ parsed: parsed({ source: 'cam' }) }));
    expect(flags.map((f) => f.key)).toContain('cam');
    expect(flags.find((f) => f.key === 'cam')?.tone).toBe('danger');
    expect(isFlagged(release({ parsed: parsed({ source: 'cam' }) }))).toBe(true);
  });

  it('flags a torrent with no seeders', () => {
    expect(releaseFlags(release({ seeders: 0 })).map((f) => f.key)).toContain('no-seeds');
  });

  it('does not flag a Usenet release for having no seeders', () => {
    const usenet = release({ protocol: 'usenet', seeders: 0 });
    expect(releaseFlags(usenet).map((f) => f.key)).not.toContain('no-seeds');
    expect(isFlagged(usenet)).toBe(false);
  });

  it('flags hardcoded subtitles from the release name', () => {
    const hc = release({ title: 'Some.Movie.2026.1080p.HC.WEBRip.x264-GRP' });
    expect(releaseFlags(hc).map((f) => f.key)).toContain('hardcoded');
  });

  it('does not read HC out of the middle of a word', () => {
    const notHC = release({ title: 'Some.Movie.2026.1080p.WEBRip.x264-ARCHC' });
    expect(releaseFlags(notHC).map((f) => f.key)).not.toContain('hardcoded');
  });

  it('warns about DTS audio without calling it dangerous', () => {
    const dts = release({ parsed: parsed({ audio: 'DTS-HD MA' }) });
    const flag = releaseFlags(dts).find((f) => f.key === 'dts');
    expect(flag?.tone).toBe('warning');
    // A warning must stay grabbable — only danger de-emphasizes a row.
    expect(isFlagged(dts)).toBe(false);
  });

  it('gives every flag a title, because a badge alone explains nothing', () => {
    const messy = release({
      title: 'Some.Movie.2026.HC.CAM.x264',
      seeders: 0,
      parsed: parsed({ source: 'cam', audio: 'DTS' }),
    });
    const flags = releaseFlags(messy);
    expect(flags.length).toBe(4);
    for (const flag of flags) expect(flag.title.length).toBeGreaterThan(10);
  });
});

describe('sortReleases', () => {
  it('puts the best release first and leaves the input alone', () => {
    const input = [
      release({ guid: 'a', parsed: parsed({ quality: '720p' }) }),
      release({ guid: 'b', parsed: parsed({ quality: '2160p' }) }),
      release({ guid: 'c', parsed: parsed({ quality: '1080p' }) }),
    ];
    const sorted = sortReleases(input);
    expect(sorted.map((r) => r.guid)).toEqual(['b', 'c', 'a']);
    expect(input.map((r) => r.guid)).toEqual(['a', 'b', 'c']);
  });

  it('is deterministic when scores tie', () => {
    const input = [
      release({ guid: 'z', title: 'Zebra' }),
      release({ guid: 'a', title: 'Aardvark' }),
    ];
    expect(sortReleases(input).map((r) => r.title)).toEqual(['Aardvark', 'Zebra']);
  });

  it('sorts flagged releases to the bottom', () => {
    const input = [
      release({ guid: 'cam', parsed: parsed({ quality: '2160p', source: 'cam' }) }),
      release({ guid: 'ok', parsed: parsed({ quality: '720p' }) }),
    ];
    expect(sortReleases(input)[0]?.guid).toBe('ok');
  });
});
