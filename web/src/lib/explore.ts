/**
 * Explore's filter model: the scope row, the two filter shapes, and the
 * translation between a filter, the URL that addresses it, and the query the
 * API is asked. Pure — no DOM, no I/O, unit-tested in explore.test.ts.
 *
 * THE URL IS THE STATE. Every control on the filter rail writes here and the
 * screens read back from here, so a filtered view is a link somebody can send
 * and a reload restores exactly what was on screen. That is why this module
 * exists at all: three screens, two dialects and a shareable URL is four places
 * a filter could drift apart, and here it is one.
 *
 * A filter value that names something — a genre, an actor, a performer — is a
 * `FilterRef`: an opaque id plus the name the typeahead handed over. The name
 * travels in the URL (`878:Sci-Fi`) so a reloaded chip is labelled without a
 * second lookup, and it is stripped on the way to TMDB, whose id lists have no
 * room for one. The adult endpoint takes the pair verbatim — its provider
 * filters on `performers[84060]=Mia Malkova` — so there it is sent as written.
 */

import { languageName } from './discover';
import type { MediaType } from './api/types';
import {
  currentLocale,
  translate,
  translatePlural,
  type TranslationKey,
} from './i18n.svelte';

export type ExploreScope = 'featured' | 'movies' | 'series' | 'adult';

/**
 * The four scopes, in the order the row renders them. `adult` carries the flag
 * rather than being appended by the caller, so "which pills exist" is one
 * filtered list rather than a conditional in every screen that draws the row.
 */
export const EXPLORE_SCOPES: { key: ExploreScope; label: string; adult?: true }[] = [
  { key: 'featured', get label() { return translate('explore.scope.featured'); } },
  { key: 'movies', get label() { return translate('explore.scope.movies'); } },
  { key: 'series', get label() { return translate('explore.scope.series'); } },
  { key: 'adult', get label() { return translate('explore.scope.adult'); }, adult: true },
];

/** Featured is /discover itself — the screen this row was added to. */
export function exploreScopeHref(scope: ExploreScope): string {
  return scope === 'featured' ? '/discover' : `/discover/${scope}`;
}

/**
 * The pills this reader gets. An ungranted reader is not shown a disabled
 * Adult pill — the module is invisible, not switched off (SPEC §12), and a
 * greyed-out pill announces it exists.
 */
export function visibleScopes(adultVisible: boolean): typeof EXPLORE_SCOPES {
  return EXPLORE_SCOPES.filter((scope) => !scope.adult || adultVisible);
}

export interface FilterRef {
  /** Opaque. A TMDB integer as a string, or a stash-box uuid. */
  id: string;
  /** "" when the URL carried a bare id; the chip then shows the id. */
  name: string;
}

/** Read one `id` or `id:Name`; null when there is no id to read. */
export function parseRef(raw: string): FilterRef | null {
  const cut = raw.indexOf(':');
  const id = (cut === -1 ? raw : raw.slice(0, cut)).trim();
  if (id === '') return null;
  return { id, name: cut === -1 ? '' : raw.slice(cut + 1).trim() };
}

/** The URL spelling of a ref. */
export function refParam(ref: FilterRef): string {
  return ref.name === '' ? ref.id : `${ref.id}:${ref.name}`;
}

/** What a chip shows. A ref with no name has only its id to offer. */
export function refLabel(ref: FilterRef): string {
  return ref.name === '' ? ref.id : ref.name;
}

/** Refs are a set keyed by id: picking the same person twice is one filter. */
export function toggleRef(refs: readonly FilterRef[], ref: FilterRef): FilterRef[] {
  return refs.some((r) => r.id === ref.id)
    ? refs.filter((r) => r.id !== ref.id)
    : [...refs, ref];
}

export function hasRef(refs: readonly FilterRef[], id: string): boolean {
  return refs.some((r) => r.id === id);
}

/*
 * One sort list per scope, each entry a (sort, order) pair under a name a
 * reader recognises: "Newest" rather than "release_date, descending".
 */

export type SortOrder = 'asc' | 'desc';

export interface SortChoice {
  key: string;
  label: string;
  sort: string;
  order: SortOrder;
}

/**
 * The title scopes' sorts. `revenue` is deliberately absent: TMDB serves it for
 * movies and not for series, and a control that works in one scope and silently
 * does nothing in the other is the exact failure this phase is built to avoid.
 */
export const TITLE_SORTS: SortChoice[] = [
  { key: 'popularity', get label() { return translate('explore.sort.popularity'); }, sort: 'popularity', order: 'desc' },
  { key: 'newest', get label() { return translate('explore.sort.newest'); }, sort: 'release_date', order: 'desc' },
  { key: 'oldest', get label() { return translate('explore.sort.oldest'); }, sort: 'release_date', order: 'asc' },
  { key: 'rating', get label() { return translate('explore.sort.highestRated'); }, sort: 'rating', order: 'desc' },
  { key: 'votes', get label() { return translate('explore.sort.mostVoted'); }, sort: 'votes', order: 'desc' },
  { key: 'title', get label() { return translate('explore.sort.titleAZ'); }, sort: 'title', order: 'asc' },
];

/**
 * The scene scope's sorts. "Relevance" only means anything beside a text query
 * and ignores the direction — the provider says so, and the rail says so too
 * rather than offering a control that quietly does nothing.
 */
export const SCENE_SORTS: SortChoice[] = [
  { key: 'newest', get label() { return translate('explore.sort.newest'); }, sort: 'released', order: 'desc' },
  { key: 'oldest', get label() { return translate('explore.sort.oldest'); }, sort: 'released', order: 'asc' },
  { key: 'added', get label() { return translate('explore.sort.recentlyAdded'); }, sort: 'created', order: 'desc' },
  { key: 'updated', get label() { return translate('explore.sort.recentlyUpdated'); }, sort: 'updated', order: 'desc' },
  { key: 'duration', get label() { return translate('explore.sort.longest'); }, sort: 'duration', order: 'desc' },
  { key: 'relevance', get label() { return translate('explore.sort.relevance'); }, sort: 'relevance', order: 'desc' },
];

/** Read a (sort, order) pair back to its key; the default when it is neither. */
function sortKeyOf(choices: SortChoice[], sort: string, order: string): string {
  const first = choices[0] as SortChoice;
  if (sort === '') return first.key;
  const match = choices.find((c) => c.sort === sort && (order === '' ? c.order === 'desc' : c.order === order));
  return match?.key ?? first.key;
}

function sortChoice(choices: SortChoice[], key: string): SortChoice {
  return choices.find((c) => c.key === key) ?? (choices[0] as SortChoice);
}

export interface TitleFilter {
  genres: FilterRef[];
  keywords: FilterRef[];
  /** Production companies. "Studio" on the rail — TMDB calls them companies. */
  companies: FilterRef[];
  /** Series only. The API refuses it on /discover/movies. */
  networks: FilterRef[];
  /**
   * Movies only, and the seam this phase documents: TMDB's /discover/tv has no
   * with_cast, with_crew or with_people, so the API answers 400 rather than
   * ignoring one. The rail therefore renders this pill on movies alone.
   */
  people: FilterRef[];
  /** Release/first-air window, "YYYY-MM-DD"; "" when open-ended. */
  from: string;
  to: string;
  /** Minutes; 0 is "unset", which is why a negative is never stored. */
  runtimeMin: number;
  runtimeMax: number;
  /** 0-10; 0 is unset. */
  ratingMin: number;
  /** ISO 639-1 original language; "" for any. */
  language: string;
  sort: string;
  /**
   * Client-side, as it is on every other discover screen: there is no server
   * parameter for "skip what I already have", and inventing one would page
   * differently from every other shelf.
   */
  hideOwned: boolean;
}

export const EMPTY_TITLE_FILTER: TitleFilter = {
  genres: [],
  keywords: [],
  companies: [],
  networks: [],
  people: [],
  from: '',
  to: '',
  runtimeMin: 0,
  runtimeMax: 0,
  ratingMin: 0,
  language: '',
  sort: 'popularity',
  hideOwned: false,
};

export type SceneSiteScope = 'site' | 'parent' | 'network';

export const SCENE_SITE_SCOPES: { key: SceneSiteScope; label: string; hint: string }[] = [
  {
    key: 'site',
    get label() { return translate('explore.siteScope.site.label'); },
    get hint() { return translate('explore.siteScope.site.hint'); },
  },
  {
    key: 'parent',
    get label() { return translate('explore.siteScope.parent.label'); },
    get hint() { return translate('explore.siteScope.parent.hint'); },
  },
  {
    key: 'network',
    get label() { return translate('explore.siteScope.network.label'); },
    get hint() { return translate('explore.siteScope.network.hint'); },
  },
];

export interface SceneFilter {
  /** Free text. A blank one is legal and asks for the provider's newest. */
  text: string;
  /** The site to scope to; null for the whole catalogue. */
  site: FilterRef | null;
  /** Widening ladder. Meaningless — and a 400 — without a site. */
  scope: SceneSiteScope;
  performers: FilterRef[];
  /** true asks for scenes with ALL of them; false, any. */
  performersAll: boolean;
  tags: FilterRef[];
  tagsAll: boolean;
  /** Release year; 0 unset. */
  year: number;
  /**
   * Minutes. ONE value, not a range: the provider serves a single `duration`
   * with no comparison operator, so a min/max pair would be a control that
   * cannot be honoured.
   */
  duration: number;
  sort: string;
  hideOwned: boolean;
}

export const EMPTY_SCENE_FILTER: SceneFilter = {
  text: '',
  site: null,
  scope: 'site',
  performers: [],
  performersAll: false,
  tags: [],
  tagsAll: false,
  year: 0,
  duration: 0,
  sort: 'newest',
  hideOwned: false,
};

/*
 * URL ⇄ filter.
 *
 * `hide` is the one client-only parameter. It is stripped on the way to the
 * API, which allowlists its query string and answers 400 to anything it does
 * not serve — so a parameter that only the browser understands must never be
 * forwarded.
 */

const HIDE_PARAM = 'hide';
/** Client-only: the unfiltered movies/series/adult path is a shelf landing. */
const VIEW_PARAM = 'view';
const VIEW_GRID = 'grid';

function readRefs(params: URLSearchParams, key: string): FilterRef[] {
  const out: FilterRef[] = [];
  for (const raw of params.getAll(key)) {
    const ref = parseRef(raw);
    if (ref && !hasRef(out, ref.id)) out.push(ref);
  }
  return out;
}

/** A whole number of `min` or more; the fallback when the value is not one. */
function readNumber(params: URLSearchParams, key: string, min: number, fallback: number): number {
  const raw = (params.get(key) ?? '').trim();
  if (raw === '') return fallback;
  const n = Number(raw);
  return Number.isFinite(n) && n >= min ? n : fallback;
}

/** "YYYY-MM-DD" or "". Anything else is dropped rather than sent on to 400. */
function readDate(params: URLSearchParams, key: string): string {
  const raw = (params.get(key) ?? '').trim();
  return /^\d{4}-\d{2}-\d{2}$/.test(raw) ? raw : '';
}

function readFlag(params: URLSearchParams, key: string): boolean {
  return (params.get(key) ?? '') === 'true';
}

/**
 * The scope is a parameter because two of these filters belong to one scope
 * each: `people` is a 400 on the series scope and `networks` is a 400 on the
 * movie scope (see titleApiQuery). Dropping the inapplicable one HERE — rather
 * than on the way to the API — is what keeps the URL, the chip row and the wire
 * query saying the same thing. Read without the scope, /discover/series?people=…
 * parsed into a filter that was re-emitted on every apply, sent to nobody, and
 * drawn as no chip: an invisible filter with no way to remove it.
 */
export function parseTitleFilter(mediaType: MediaType, params: URLSearchParams): TitleFilter {
  return {
    genres: readRefs(params, 'genres'),
    keywords: readRefs(params, 'keywords'),
    companies: readRefs(params, 'companies'),
    networks: mediaType === 'series' ? readRefs(params, 'networks') : [],
    people: mediaType === 'movie' ? readRefs(params, 'people') : [],
    from: readDate(params, 'from'),
    to: readDate(params, 'to'),
    runtimeMin: Math.trunc(readNumber(params, 'runtime_min', 0, 0)),
    runtimeMax: Math.trunc(readNumber(params, 'runtime_max', 0, 0)),
    ratingMin: Math.min(10, readNumber(params, 'rating_min', 0, 0)),
    language: (params.get('language') ?? '').trim(),
    sort: sortKeyOf(TITLE_SORTS, (params.get('sort') ?? '').trim(), (params.get('order') ?? '').trim()),
    hideOwned: params.get(HIDE_PARAM) === '1',
  };
}

/**
 * The URL for a filter, as a query string without its leading '?'.
 *
 * Only what is set is written: an empty filter is an empty string, so
 * /discover/movies and /discover/movies? are the same address and the scope
 * pill's plain href is the canonical one.
 */
export function titleFilterQuery(mediaType: MediaType, filter: TitleFilter): string {
  const params = new URLSearchParams();
  // The mirror of parseTitleFilter: the scope that cannot serve a list does not
  // write one either, so a stale parameter is gone after the next apply rather
  // than being copied forward forever.
  const refs: [string, FilterRef[]][] = [
    ['genres', filter.genres],
    ['keywords', filter.keywords],
    ['companies', filter.companies],
    ['networks', mediaType === 'series' ? filter.networks : []],
    ['people', mediaType === 'movie' ? filter.people : []],
  ];
  for (const [key, list] of refs) {
    for (const ref of list) params.append(key, refParam(ref));
  }
  if (filter.from) params.set('from', filter.from);
  if (filter.to) params.set('to', filter.to);
  if (filter.runtimeMin > 0) params.set('runtime_min', String(filter.runtimeMin));
  if (filter.runtimeMax > 0) params.set('runtime_max', String(filter.runtimeMax));
  if (filter.ratingMin > 0) params.set('rating_min', String(filter.ratingMin));
  if (filter.language) params.set('language', filter.language);
  const choice = sortChoice(TITLE_SORTS, filter.sort);
  if (choice.key !== (TITLE_SORTS[0] as SortChoice).key) {
    params.set('sort', choice.sort);
    params.set('order', choice.order);
  }
  if (filter.hideOwned) params.set(HIDE_PARAM, '1');
  return params.toString();
}

export function parseSceneFilter(params: URLSearchParams): SceneFilter {
  const site = parseRef(params.get('site') ?? '');
  const scopeRaw = (params.get('scope') ?? '').trim();
  const scope = SCENE_SITE_SCOPES.some((s) => s.key === scopeRaw)
    ? (scopeRaw as SceneSiteScope)
    : 'site';
  return {
    text: (params.get('q') ?? '').trim(),
    site,
    // A widened scope with no site to widen is a 400 at the API, so it is
    // narrowed back here rather than carried into a request that cannot work.
    scope: site === null ? 'site' : scope,
    performers: readRefs(params, 'performers'),
    performersAll: readFlag(params, 'performers_all'),
    tags: readRefs(params, 'tags'),
    tagsAll: readFlag(params, 'tags_all'),
    year: Math.trunc(readNumber(params, 'year', 0, 0)),
    duration: Math.trunc(readNumber(params, 'duration', 0, 0)),
    sort: sortKeyOf(SCENE_SORTS, (params.get('sort') ?? '').trim(), (params.get('order') ?? '').trim()),
    hideOwned: params.get(HIDE_PARAM) === '1',
  };
}

export function sceneFilterQuery(filter: SceneFilter): string {
  const params = new URLSearchParams();
  if (filter.text) params.set('q', filter.text);
  if (filter.site) {
    params.set('site', refParam(filter.site));
    if (filter.scope !== 'site') params.set('scope', filter.scope);
  }
  for (const ref of filter.performers) params.append('performers', refParam(ref));
  if (filter.performersAll && filter.performers.length > 0) params.set('performers_all', 'true');
  for (const ref of filter.tags) params.append('tags', refParam(ref));
  if (filter.tagsAll && filter.tags.length > 0) params.set('tags_all', 'true');
  if (filter.year > 0) params.set('year', String(filter.year));
  if (filter.duration > 0) params.set('duration', String(filter.duration));
  const choice = sortChoice(SCENE_SORTS, filter.sort);
  if (choice.key !== (SCENE_SORTS[0] as SortChoice).key) {
    params.set('sort', choice.sort);
    params.set('order', choice.order);
  }
  if (filter.hideOwned) params.set(HIDE_PARAM, '1');
  return params.toString();
}

/** The full link for a filtered scope — what the rail pushes into history. */
export function titleFilterHref(mediaType: MediaType, filter: TitleFilter): string {
  const query = titleFilterQuery(mediaType, filter);
  const path = mediaType === 'movie' ? '/discover/movies' : '/discover/series';
  return query === '' ? path : `${path}?${query}`;
}

export function sceneFilterHref(filter: SceneFilter): string {
  const query = sceneFilterQuery(filter);
  return query === '' ? '/discover/adult' : `/discover/adult?${query}`;
}

/**
 * The unfiltered movies/series path is the editorial landing. `view=grid` is
 * the infinite popular list that used to be that path; it is client-only, so
 * it must never ride along to the API the way `hide` must not.
 */
export function isTitleLanding(
  mediaType: MediaType,
  filter: TitleFilter,
  params: URLSearchParams,
): boolean {
  return (
    titleChips(mediaType, filter).length === 0 &&
    filter.sort === 'popularity' &&
    params.get(VIEW_PARAM) !== VIEW_GRID
  );
}

export function isSceneLanding(filter: SceneFilter, params: URLSearchParams): boolean {
  return (
    sceneChips(filter).length === 0 &&
    filter.sort === 'newest' &&
    params.get(VIEW_PARAM) !== VIEW_GRID
  );
}

export function titleGridHref(mediaType: MediaType): string {
  const path = mediaType === 'movie' ? '/discover/movies' : '/discover/series';
  return `${path}?${VIEW_PARAM}=${VIEW_GRID}`;
}

export function sceneGridHref(): string {
  return `/discover/adult?${VIEW_PARAM}=${VIEW_GRID}`;
}

/** Today's UTC date as YYYY-MM-DD — the bound "upcoming" asks the API. */
export function utcDateString(now = new Date()): string {
  const year = now.getUTCFullYear();
  const month = String(now.getUTCMonth() + 1).padStart(2, '0');
  const day = String(now.getUTCDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

export function titleUpcomingHref(mediaType: MediaType, now = new Date()): string {
  return titleFilterHref(mediaType, {
    ...EMPTY_TITLE_FILTER,
    from: utcDateString(now),
    sort: 'newest',
  });
}

export function titleGenreHref(mediaType: MediaType, genre: FilterRef): string {
  return titleFilterHref(mediaType, { ...EMPTY_TITLE_FILTER, genres: [genre] });
}

export function sceneSiteHref(site: FilterRef): string {
  return sceneFilterHref({ ...EMPTY_SCENE_FILTER, site });
}

export function sceneAddedHref(): string {
  return sceneFilterHref({ ...EMPTY_SCENE_FILTER, sort: 'added' });
}

/*
 * Filter → API query.
 *
 * The two dialects differ in one way that matters: TMDB takes comma-joined bare
 * ids, and the adult endpoint takes one repeated parameter per ref with the
 * name attached, because a performer's name may contain a comma and its
 * provider filters on the name as well as the id.
 */

export type ApiQuery = Record<string, string | number | string[] | undefined>;

const SECONDS_PER_MINUTE = 60;

function idList(refs: readonly FilterRef[]): string | undefined {
  return refs.length === 0 ? undefined : refs.map((r) => r.id).join(',');
}

/**
 * What GET /discover/{movies,series} is asked. `hideOwned` is absent by
 * design — it is a view over the answer, not a question for the provider.
 *
 * The scope decides which of the two exclusive filters is sent: `people` is a
 * 400 on the series scope and `networks` is a 400 on the movie scope, so
 * neither is ever sent to the one that refuses it.
 */
export function titleApiQuery(
  mediaType: MediaType,
  filter: TitleFilter,
  page: number,
): ApiQuery {
  const choice = sortChoice(TITLE_SORTS, filter.sort);
  return {
    genres: idList(filter.genres),
    keywords: idList(filter.keywords),
    companies: idList(filter.companies),
    networks: mediaType === 'series' ? idList(filter.networks) : undefined,
    people: mediaType === 'movie' ? idList(filter.people) : undefined,
    from: filter.from || undefined,
    to: filter.to || undefined,
    runtime_min: filter.runtimeMin > 0 ? filter.runtimeMin : undefined,
    runtime_max: filter.runtimeMax > 0 ? filter.runtimeMax : undefined,
    rating_min: filter.ratingMin > 0 ? filter.ratingMin : undefined,
    language: filter.language || undefined,
    sort: choice.sort,
    order: choice.order,
    page,
  };
}

/** What GET /adult/discover is asked. */
export function sceneApiQuery(filter: SceneFilter, page: number): ApiQuery {
  const choice = sortChoice(SCENE_SORTS, filter.sort);
  return {
    q: filter.text || undefined,
    site: filter.site?.id,
    scope: filter.site && filter.scope !== 'site' ? filter.scope : undefined,
    performers: filter.performers.map(refParam),
    performers_all: filter.performersAll && filter.performers.length > 0 ? 'true' : undefined,
    tags: filter.tags.map(refParam),
    tags_all: filter.tagsAll && filter.tags.length > 0 ? 'true' : undefined,
    year: filter.year > 0 ? filter.year : undefined,
    // Minutes on the rail and in the URL, because that is what a run time is
    // read in; seconds on the wire, because that is what the provider counts.
    duration: filter.duration > 0 ? filter.duration * SECONDS_PER_MINUTE : undefined,
    sort: choice.sort,
    order: choice.order,
    page,
  };
}

/*
 * Applied chips.
 *
 * One chip is one removable fact. A range is a single chip because "runtime
 * 60-120" is one thought, and two chips for it would let somebody remove half a
 * bound and be left with a filter they did not choose.
 */

export interface AppliedChip {
  /** Stable, and the argument `removeTitleChip`/`removeSceneChip` takes back. */
  key: string;
  label: string;
}

function refChips(
  prefix: string,
  nounKey: TranslationKey,
  refs: readonly FilterRef[],
): AppliedChip[] {
  return refs.map((ref) => ({
    key: `${prefix}:${ref.id}`,
    label: translate('explore.chip.named', {
      name: translate(nounKey),
      value: refLabel(ref),
    }),
  }));
}

function rangeText(min: number, max: number, unit: string): string {
  if (min > 0 && max > 0) return translate('explore.range.between', { min, max, unit });
  if (min > 0) return translate('explore.range.from', { min, unit });
  return translate('explore.range.under', { max, unit });
}

function dateWindowText(from: string, to: string): string {
  const a = from.slice(0, 4);
  const b = to.slice(0, 4);
  if (a && b) return a === b ? a : translate('explore.date.between', { from: a, to: b });
  return a ? translate('explore.date.from', { from: a }) : translate('explore.date.until', { to: b });
}

export function titleChips(mediaType: MediaType, filter: TitleFilter): AppliedChip[] {
  const chips: AppliedChip[] = [
    ...refChips('genres', 'explore.chip.genre', filter.genres),
    ...(mediaType === 'movie'
      ? refChips('people', 'explore.chip.castCrew', filter.people)
      : []),
    ...(mediaType === 'series'
      ? refChips('networks', 'explore.chip.network', filter.networks)
      : []),
    ...refChips('companies', 'explore.chip.studio', filter.companies),
    ...refChips('keywords', 'explore.chip.keyword', filter.keywords),
  ];
  if (filter.from || filter.to) {
    chips.push({
      key: 'dates',
      label: translate('explore.chip.named', {
        name: translate('explore.chip.year'),
        value: dateWindowText(filter.from, filter.to),
      }),
    });
  }
  if (filter.runtimeMin > 0 || filter.runtimeMax > 0) {
    chips.push({
      key: 'runtime',
      label: translate('explore.chip.named', {
        name: translate('explore.chip.runtime'),
        value: rangeText(
          filter.runtimeMin,
          filter.runtimeMax,
          translate('explore.unit.minute'),
        ),
      }),
    });
  }
  if (filter.ratingMin > 0) {
    chips.push({
      key: 'rating',
      label: translate('explore.chip.named', {
        name: translate('explore.chip.rating'),
        value: `${filter.ratingMin}+`,
      }),
    });
  }
  if (filter.language) {
    chips.push({
      key: 'language',
      label: translate('explore.chip.named', {
        name: translate('explore.chip.language'),
        value: languageName(filter.language),
      }),
    });
  }
  return chips;
}

export function removeTitleChip(filter: TitleFilter, key: string): TitleFilter {
  const [group, id] = splitChipKey(key);
  switch (group) {
    case 'genres':
      return { ...filter, genres: filter.genres.filter((r) => r.id !== id) };
    case 'people':
      return { ...filter, people: filter.people.filter((r) => r.id !== id) };
    case 'networks':
      return { ...filter, networks: filter.networks.filter((r) => r.id !== id) };
    case 'companies':
      return { ...filter, companies: filter.companies.filter((r) => r.id !== id) };
    case 'keywords':
      return { ...filter, keywords: filter.keywords.filter((r) => r.id !== id) };
    case 'dates':
      return { ...filter, from: '', to: '' };
    case 'runtime':
      return { ...filter, runtimeMin: 0, runtimeMax: 0 };
    case 'rating':
      return { ...filter, ratingMin: 0 };
    case 'language':
      return { ...filter, language: '' };
    default:
      return filter;
  }
}

export function sceneChips(filter: SceneFilter): AppliedChip[] {
  const chips: AppliedChip[] = [];
  if (filter.text) {
    chips.push({
      key: 'q',
      label: translate('explore.chip.named', {
        name: translate('explore.chip.search'),
        value: filter.text,
      }),
    });
  }
  if (filter.site) {
    const widened = SCENE_SITE_SCOPES.find((s) => s.key === filter.scope);
    const label =
      filter.scope === 'site'
        ? translate('explore.chip.named', {
            name: translate('explore.chip.site'),
            value: refLabel(filter.site),
          })
        : translate('explore.chip.siteScoped', {
            site: refLabel(filter.site),
            scope: widened?.label ?? '',
          });
    chips.push({ key: 'site', label });
  }
  chips.push(...refChips('performers', 'explore.chip.performer', filter.performers));
  chips.push(...refChips('tags', 'explore.chip.tag', filter.tags));
  if (filter.year > 0) {
    chips.push({
      key: 'year',
      label: translate('explore.chip.named', {
        name: translate('explore.chip.year'),
        value: filter.year,
      }),
    });
  }
  if (filter.duration > 0) {
    chips.push({
      key: 'duration',
      label: translate('explore.chip.named', {
        name: translate('explore.chip.duration'),
        value: translatePlural('discover.runtime.minute', filter.duration),
      }),
    });
  }
  return chips;
}

export function removeSceneChip(filter: SceneFilter, key: string): SceneFilter {
  const [group, id] = splitChipKey(key);
  switch (group) {
    case 'q':
      return { ...filter, text: '' };
    case 'site':
      // Dropping the site drops the widening with it: `scope` without a site is
      // a 400, and silently keeping it would break the next search.
      return { ...filter, site: null, scope: 'site' };
    case 'performers': {
      const performers = filter.performers.filter((r) => r.id !== id);
      return { ...filter, performers, performersAll: performers.length > 1 && filter.performersAll };
    }
    case 'tags': {
      const tags = filter.tags.filter((r) => r.id !== id);
      return { ...filter, tags, tagsAll: tags.length > 1 && filter.tagsAll };
    }
    case 'year':
      return { ...filter, year: 0 };
    case 'duration':
      return { ...filter, duration: 0 };
    default:
      return filter;
  }
}

/** "genres:878" → ["genres", "878"]; "runtime" → ["runtime", ""]. */
function splitChipKey(key: string): [string, string] {
  const cut = key.indexOf(':');
  return cut === -1 ? [key, ''] : [key.slice(0, cut), key.slice(cut + 1)];
}

/**
 * What "Clear all" leaves behind. Sort and the hide toggle survive: neither is
 * a chip, so clearing them would remove something the button does not appear to
 * be about.
 */
export function clearedTitleFilter(filter: TitleFilter): TitleFilter {
  return { ...EMPTY_TITLE_FILTER, sort: filter.sort, hideOwned: filter.hideOwned };
}

export function clearedSceneFilter(filter: SceneFilter): SceneFilter {
  return { ...EMPTY_SCENE_FILTER, sort: filter.sort, hideOwned: filter.hideOwned };
}

/*
 * The language pill.
 *
 * A short list rather than TMDB's several hundred: the pill is a filter, not a
 * catalogue, and every code here is one the provider has a meaningful amount of
 * in. The labels come from the runtime, so they arrive in the reader's own
 * language.
 */

export const FILTER_LANGUAGES = [
  'en', 'es', 'fr', 'de', 'it', 'pt', 'nl', 'sv', 'da', 'no', 'pl', 'ru',
  'ja', 'ko', 'zh', 'hi', 'ar', 'tr', 'th',
] as const;

export function languageOptions(): { code: string; label: string }[] {
  return FILTER_LANGUAGES.map((code) => ({ code, label: languageName(code) })).sort((a, b) =>
    a.label.localeCompare(b.label),
  );
}

/**
 * "24,861 movies match" — the line under the scope row. The verb agrees with
 * the noun, so the singular reads "1 scene matches" rather than the
 * ungrammatical "1 scene match".
 */
export function matchCountLine(total: number, noun: 'movie' | 'series' | 'scene'): string {
  if (!Number.isFinite(total) || total <= 0) return '';
  return translatePlural(`explore.match.${noun}`, total, {
    count: new Intl.NumberFormat(currentLocale()).format(total),
  });
}

/** The placeholder in the scene year box — this year, so the shape is obvious. */
export function sceneYearNow(): number {
  return new Date().getFullYear();
}

/** A scene card's duration badge — "41:12". Null when the provider has none. */
export function durationBadge(seconds: number): string | null {
  if (!Number.isFinite(seconds) || seconds <= 0) return null;
  const whole = Math.trunc(seconds);
  const hours = Math.trunc(whole / 3600);
  const minutes = Math.trunc((whole % 3600) / 60);
  const rest = whole % 60;
  const pad = (n: number) => String(n).padStart(2, '0');
  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(rest)}` : `${minutes}:${pad(rest)}`;
}
