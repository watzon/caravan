/**
 * Path matching for the hand-rolled router. Pure — unit-tested in
 * router.test.ts. The stateful half (history, click interception) lives in
 * router.svelte.ts.
 */

/**
 * Every route the SPA serves, in match order.
 *
 * The interactive release picker is a screen, not a modal (DESIGN.md §5), so
 * its target lives in the path rather than a query string: the router
 * deliberately drops query strings, and a picker you cannot link to or reload
 * is not a first-class screen.
 */
export const ROUTES = [
  '/first-run',
  // Explore. The index is Discover: the first question on opening Caravan is
  // "what should I watch", not "what do I already have". /discover is the same
  // screen under its own name, so the nav entry has a canonical href.
  '/',
  '/discover',
  '/discover/network/:id',
  '/discover/studio/:id',
  // TMDB ids, not library ids — a discover detail is about a title Caravan may
  // not track at all.
  '/discover/movie/:tmdbId',
  '/discover/series/:tmdbId',
  '/requests',
  '/movies',
  '/movies/:id',
  '/movies/:id/search',
  '/series',
  '/series/:id',
  // The whole-series picker, mirroring /movies/:id/search. The narrower forms
  // below carry the season (and optionally the episode) that scopes the query.
  '/series/:id/search',
  '/series/:id/search/:season',
  '/series/:id/search/:season/:episode',
  // The adult module (phase 9). Every one of these is behind `isAdultRoute`
  // as well as the member allowlist: the module is invisible to an account it
  // was not granted to, whatever that account's role is.
  '/adult',
  '/adult/scenes',
  '/adult/sites/:id',
  // The scene picker, under /adult on purpose: isAdultRoute is derived from the
  // path, so a picker filed here is gated by having been added rather than by
  // somebody remembering to name it. Filing it under /series/:id/search — which
  // would work, since a site IS a series row — would put a scene screen behind a
  // pattern the adult gate cannot see.
  '/adult/sites/:id/search',
  '/adult/sites/:id/search/:year',
  '/adult/sites/:id/search/:year/:number',
  '/queue',
  '/convert',
  '/wanted',
  '/calendar',
  '/history',
  '/scan-review',
  '/settings',
  '/settings/:section',
] as const;

export type RoutePattern = (typeof ROUTES)[number];

/**
 * The screens a member may open (SPEC §11): find something, and ask for it.
 *
 * It mirrors the server's own allowlist in internal/api/auth.go — everything
 * absent from it answers 403 for a member — so this is an allowlist too rather
 * than a list of admin screens. A route added tomorrow is closed to members
 * until somebody names it here, which is the safe direction to be wrong in.
 */
export const MEMBER_ROUTES: readonly RoutePattern[] = [
  '/',
  '/discover',
  '/discover/network/:id',
  '/discover/studio/:id',
  '/discover/movie/:tmdbId',
  '/discover/series/:tmdbId',
  '/requests',
  // The adult screens, which a member reaches only with the grant — that
  // second condition is `isAdultRoute` below, not this list. Being named here
  // only says "a member is not barred from this by their role"; it mirrors
  // internal/api/auth.go memberAllowed, which names the same three reads.
  '/adult',
  '/adult/scenes',
  '/adult/sites/:id',
  // The scene picker is deliberately absent: grabbing is an admin write, and
  // the server answers a member's release search the same way it answers every
  // other admin route.
];

/** Whether a member may open this route. Pure — the guard lives in App.svelte. */
export function memberAllowedRoute(pattern: RoutePattern): boolean {
  return MEMBER_ROUTES.includes(pattern);
}

/**
 * The routes that only exist while the adult module is visible to the caller
 * (SessionUser.adult).
 *
 * This is a second, independent guard rather than more entries in
 * MEMBER_ROUTES, because the two answer different questions: the member
 * allowlist is about a ROLE, and this is about a per-account grant that an
 * admin also has to have been given (by switching the module on). An admin who
 * turned it off must land nowhere near these screens either — which the member
 * allowlist, being role-shaped, could never express.
 *
 * Derived from the path so a route added under /adult tomorrow is gated by
 * having been added, not by somebody remembering to name it twice.
 */
export function isAdultRoute(pattern: RoutePattern): boolean {
  return pattern === '/adult' || pattern.startsWith('/adult/');
}

export interface RouteMatch {
  pattern: RoutePattern;
  params: Record<string, string>;
}

/** Leading slash, no trailing slash, no query or hash. */
export function normalizePath(path: string): string {
  const cut = path.replace(/[?#].*$/, '');
  const withLeading = cut.startsWith('/') ? cut : `/${cut}`;
  const trimmed = withLeading.replace(/\/+$/, '');
  return trimmed === '' ? '/' : trimmed;
}

/**
 * Match one pattern against a normalized path. Returns the captured `:params`,
 * or null when the pattern does not apply.
 */
export function matchPath(pattern: string, path: string): Record<string, string> | null {
  const patternParts = normalizePath(pattern).split('/');
  const pathParts = normalizePath(path).split('/');
  if (patternParts.length !== pathParts.length) return null;

  const params: Record<string, string> = {};
  for (let i = 0; i < patternParts.length; i++) {
    const p = patternParts[i] as string;
    const v = pathParts[i] as string;
    if (p.startsWith(':')) {
      if (v === '') return null;
      params[p.slice(1)] = decodeURIComponent(v);
      continue;
    }
    if (p !== v) return null;
  }
  return params;
}

/** First matching route, or null when nothing matches. */
export function matchRoutes(
  patterns: readonly RoutePattern[],
  path: string,
): RouteMatch | null {
  for (const pattern of patterns) {
    const params = matchPath(pattern, path);
    if (params) return { pattern, params };
  }
  return null;
}

/**
 * Parse a season or episode number out of the path. Unlike an id, 0 is a real
 * value here — season 0 is Specials (SPEC §7) — so an unparseable value answers
 * -1 rather than 0.
 */
export function ordinalParam(params: Record<string, string>, name: string): number {
  const raw = params[name];
  if (raw === undefined) return -1;
  const n = Number(raw);
  return Number.isInteger(n) && n >= 0 ? n : -1;
}

/** Parse a `:id` param; returns 0 when it is not a positive integer. */
export function numericParam(params: Record<string, string>, name: string): number {
  const raw = params[name];
  if (raw === undefined) return 0;
  const n = Number(raw);
  return Number.isInteger(n) && n > 0 ? n : 0;
}
