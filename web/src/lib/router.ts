/**
 * Path matching for the hand-rolled router. Pure — unit-tested in
 * router.test.ts. The stateful half (history, click interception) lives in
 * router.svelte.ts.
 */

/** Every route the phase-1 SPA serves, in match order. */
export const ROUTES = [
  '/first-run',
  '/movies',
  '/movies/:id',
  '/series',
  '/series/:id',
  '/scan-review',
  '/settings',
] as const;

export type RoutePattern = (typeof ROUTES)[number];

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

/** Parse a `:id` param; returns 0 when it is not a positive integer. */
export function numericParam(params: Record<string, string>, name: string): number {
  const raw = params[name];
  if (raw === undefined) return 0;
  const n = Number(raw);
  return Number.isInteger(n) && n > 0 ? n : 0;
}
