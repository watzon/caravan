/**
 * The adult module's pure helpers — and above all `adultVisible`, which is the
 * phase's whole safety property written as a truth table.
 *
 * adult.ts says of it: "a rule that can only be exercised by mounting four
 * screens as six identities is a rule nobody re-checks. Here it is a truth
 * table." This file is the half of that sentence that makes it true.
 */
import { describe, expect, it } from 'vitest';
import type { Scene, SceneMeta, SessionUser, Site } from './api/types';
import {
  ADULT_EXPLORE_HREF,
  adultVisible,
  EVERY_SCENE_FILTER,
  performerSummary,
  sceneCountNote,
  sceneDuration,
  sceneFiltersOf,
  sceneLine,
  sceneMetaLine,
  sceneNumber,
  scenePerformers,
  sceneTitleLine,
  sceneYear,
  siteHref,
  stashUnreachableBanner,
} from './adult';
import { UNKNOWN } from './format';

function user(extra: Partial<SessionUser> = {}): SessionUser {
  return { username: 'ada', role: 'member', open: false, adult: false, ...extra };
}

describe('adultVisible', () => {
  /**
   * The server has already ANDed the server-wide switch with this account's
   * grant into `SessionUser.adult`, so there is exactly one input. Every case
   * below is therefore about not consulting a second one.
   */
  it('reads the one field the server decided, and only that field', () => {
    expect(adultVisible(user({ adult: true }))).toBe(true);
    expect(adultVisible(user({ adult: false }))).toBe(false);
  });

  /**
   * The asymmetry with `session.isAdmin`, which reads an unknown identity AS an
   * admin. Guessing "admin" wrong shows a nav group whose screens 403; guessing
   * "adult" wrong shows the module on a server where somebody switched it off,
   * which is the one thing this phase promises never happens.
   */
  it('reads an unknown identity as not granted', () => {
    expect(adultVisible(null)).toBe(false);
  });

  /**
   * A role check must never stand in for the grant. An admin on a server with
   * the module switched OFF gets `adult: false`, and that answer wins — this is
   * the "no trace when disabled" rule at its most load-bearing point.
   */
  it('never lets the role override the grant', () => {
    expect(adultVisible(user({ role: 'admin', adult: false }))).toBe(false);
    expect(adultVisible(user({ role: 'admin', open: true, adult: false }))).toBe(false);
    // …and a plain member with the grant does reach it.
    expect(adultVisible(user({ role: 'member', adult: true }))).toBe(true);
  });

  /**
   * The field is documented as false-never-absent, but a body from an older
   * server would simply lack it. Missing must read as "not granted" rather than
   * as truthy-by-accident.
   */
  it('reads a missing field as not granted', () => {
    const legacy = { username: 'ada', role: 'admin', open: false } as unknown as SessionUser;
    expect(adultVisible(legacy)).toBe(false);
  });
});

describe('sceneFiltersOf', () => {
  it('reads the block the server sent', () => {
    const thin = { ...EVERY_SCENE_FILTER, year: false, site_scope: false };
    expect(sceneFiltersOf(user({ adult: true, scene_filters: thin }))).toEqual(thin);
  });

  /**
   * The asymmetry with `adultVisible`, and the one worth spelling out: absence
   * here is EVERY filter, not none.
   *
   * Three things produce an absent block — an ungranted caller, a server with
   * no stash-box credential, and a server older than the field — and in all
   * three the rail drawing a control that turns out to be unserved is the
   * behaviour that already existed: the request refuses with a 400 that names
   * the filter and offers Clear filters. Reading absence as "none" would hide
   * two thirds of the rail on a perfectly capable TPDB server whose credential
   * had merely not been set yet, which is both a worse answer and one nobody
   * could diagnose from the screen.
   */
  it('reads an absent block as every filter, not none', () => {
    expect(sceneFiltersOf(user({ adult: true }))).toEqual(EVERY_SCENE_FILTER);
    expect(sceneFiltersOf(null)).toEqual(EVERY_SCENE_FILTER);
  });
});

describe('adult shelf links', () => {
  /**
   * The Scenes tab is retired (PLAN phase 12 task 4): browsing the provider's
   * catalogue moved to Explore, beside the other two catalogues, and the shelf
   * is sites only. The tab vocabulary went with it — this is what is left, and
   * it is a link INTO Explore rather than a second tab.
   */
  it('sends "browse scenes" to the explore scope, not to a tab', () => {
    expect(ADULT_EXPLORE_HREF).toBe('/discover/adult');
  });

  it('points a site card at its own page by library id', () => {
    expect(siteHref({ id: 12 } as Site)).toBe('/adult/sites/12');
  });
});

describe('sceneNumber', () => {
  it('pads to three so a year of scenes has a straight left edge', () => {
    expect(sceneNumber(3)).toBe('#003');
    expect(sceneNumber(42)).toBe('#042');
    expect(sceneNumber(240)).toBe('#240');
  });

  // A number past 999 keeps its own width rather than being truncated.
  it('lets a four-digit scene keep its width', () => {
    expect(sceneNumber(1024)).toBe('#1024');
  });

  it('renders an unknown ordinal as a placeholder rather than #000', () => {
    expect(sceneNumber(0)).toBe(`#${UNKNOWN}`);
    expect(sceneNumber(-1)).toBe(`#${UNKNOWN}`);
    expect(sceneNumber(Number.NaN)).toBe(`#${UNKNOWN}`);
  });
});

describe('scene lines', () => {
  function scene(extra: Partial<Scene> = {}): Scene {
    return {
      id: 1,
      series_id: 2,
      year: 2022,
      number: 3,
      stash_id: 's1',
      title: 'Deep Impact',
      overview: '',
      studio: '',
      performers: ['Ava Wells', 'Ivy Rain'],
      url: '',
      provider_url: '',
      release_date: '2022-03-14',
      monitored: true,
      ...extra,
    };
  }

  it('reads title then performers, which is what somebody is looking for', () => {
    expect(sceneTitleLine(scene())).toBe('Deep Impact · Ava Wells, Ivy Rain');
    expect(sceneLine(scene())).toBe('#003 Deep Impact · Ava Wells, Ivy Rain');
  });

  it('never collapses an untitled scene to a bare number', () => {
    expect(sceneTitleLine(scene({ title: '' }))).toBe(`${UNKNOWN} · Ava Wells, Ivy Rain`);
  });

  it('drops the separator when the provider names nobody', () => {
    expect(sceneTitleLine(scene({ performers: [] }))).toBe('Deep Impact');
    // Blank names are holes in the provider's data, not performers.
    expect(sceneTitleLine(scene({ performers: ['', ''] }))).toBe('Deep Impact');
    expect(sceneTitleLine(scene({ performers: ['Ava Wells', ''] }))).toBe('Deep Impact · Ava Wells');
  });
});

describe('sceneCountNote', () => {
  function site(extra: Partial<Site> = {}): Site {
    return { scene_count: 240, scene_file_count: 18, ...extra } as Site;
  }

  it('reads held over known', () => {
    expect(sceneCountNote(site())).toBe('18 / 240 scenes');
  });

  // A site whose catalogue has not been walked yet would read "0 / 0 scenes",
  // which is a number nobody can act on.
  it('says nothing at all when the catalogue is unknown', () => {
    expect(sceneCountNote(site({ scene_count: 0, scene_file_count: 0 }))).toBeNull();
  });
});

describe('sceneYear', () => {
  it('takes the leading year of a release date', () => {
    expect(sceneYear('2022-03-14')).toBe(2022);
  });

  it('reads a missing or unparseable date as zero', () => {
    expect(sceneYear('')).toBe(0);
    expect(sceneYear(null)).toBe(0);
    expect(sceneYear(undefined)).toBe(0);
    expect(sceneYear('not-a-date')).toBe(0);
  });
});

describe('sceneDuration', () => {
  it('rounds seconds to whole minutes', () => {
    expect(sceneDuration(1440)).toBe('24 min');
    expect(sceneDuration(1470)).toBe('25 min');
  });

  /**
   * The provider reports 0 far more often than it reports a real duration, so
   * zero must render as nothing rather than as "0 min".
   */
  it('renders an unknown run time as nothing', () => {
    expect(sceneDuration(0)).toBeNull();
    expect(sceneDuration(-5)).toBeNull();
    expect(sceneDuration(Number.NaN)).toBeNull();
  });

  // A scene shorter than a minute is still a scene; it must not read "0 min".
  it('floors a very short scene at one minute', () => {
    expect(sceneDuration(20)).toBe('1 min');
  });
});

describe('sceneMetaLine', () => {
  function meta(extra: Partial<SceneMeta> = {}): SceneMeta {
    return {
      site_name: 'Deep Blue',
      date: '2022-03-14',
      duration: 1440,
      ...extra,
    } as SceneMeta;
  }

  it('joins whichever of site, date and run time the provider knows', () => {
    expect(sceneMetaLine(meta())).toBe('Deep Blue · 2022-03-14 · 24 min');
  });

  it('leaves no dangling separators when fields are missing', () => {
    expect(sceneMetaLine(meta({ duration: 0 }))).toBe('Deep Blue · 2022-03-14');
    expect(sceneMetaLine(meta({ date: '' }))).toBe('Deep Blue · 24 min');
    expect(sceneMetaLine(meta({ site_name: '' }))).toBe('2022-03-14 · 24 min');
    expect(sceneMetaLine(meta({ site_name: '', date: '', duration: 0 }))).toBe('');
  });
});

describe('performerSummary', () => {
  it('is empty when nobody is credited', () => {
    expect(performerSummary([])).toBe('');
    expect(performerSummary(['', '  '])).toBe('');
  });

  it('names one performer plainly', () => {
    expect(performerSummary(['Ava Wells'])).toBe('Ava Wells');
  });

  it('joins two with an ampersand, which is how a pairing reads', () => {
    expect(performerSummary(['Ava Wells', 'Ivy Rain'])).toBe('Ava Wells & Ivy Rain');
  });

  it('counts the rest when a cast is bigger than the column', () => {
    expect(performerSummary(['Ava Wells', 'Ivy Rain', 'Mia Stone'])).toBe('Ava Wells, Ivy Rain +1');
    expect(performerSummary(['A One', 'B Two', 'C Three', 'D Four', 'E Five'])).toBe(
      'A One, B Two +3',
    );
  });

  it('does not count the blanks a provider sometimes stores', () => {
    expect(performerSummary(['Ava Wells', '', 'Ivy Rain'])).toBe('Ava Wells & Ivy Rain');
  });
});

describe('scenePerformers', () => {
  it('drops blank credits', () => {
    expect(scenePerformers({ performers: ['Ava Wells', '', ' '] })).toEqual(['Ava Wells']);
  });

  it('treats a missing list as nobody', () => {
    expect(scenePerformers({ performers: undefined as unknown as string[] })).toEqual([]);
  });
});

describe('stashUnreachableBanner', () => {
  const NOW = Date.parse('2026-08-03T12:00:00Z');

  it('says nothing when the handoff is answering', () => {
    expect(stashUnreachableBanner(undefined, NOW)).toBeNull();
  });

  it("names the reason and how long it has been going on", () => {
    const banner = stashUnreachableBanner(
      { error: 'connection refused', since: '2026-08-03T09:00:00Z' },
      NOW,
    );
    expect(banner?.title).toBe('Stash is unreachable');
    expect(banner?.message).toContain('connection refused');
    expect(banner?.message).toContain('Unreachable for 3h');
  });

  it('promises the imports are unaffected, which is the point of the wording', () => {
    const banner = stashUnreachableBanner({ error: 'i/o timeout', since: '' }, NOW);
    expect(banner?.message).toContain('Adult imports continue.');
    expect(banner?.message).toContain('queued');
    // No parseable timestamp, no duration clause — never "Unreachable for —".
    expect(banner?.message).not.toContain('Unreachable for');
  });

  it('still banners when the server sent no reason at all', () => {
    const banner = stashUnreachableBanner({ error: '', since: '2026-08-03T11:00:00Z' }, NOW);
    expect(banner).not.toBeNull();
    // A stray leading space or a dangling "." would read as a truncated string.
    expect(banner?.message.startsWith('Unreachable for 1h.')).toBe(true);
  });
});
