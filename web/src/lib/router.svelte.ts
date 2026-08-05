/**
 * Reactive router state over the History API. The server serves the SPA with a
 * fallback to index.html, so real paths work on reload.
 */

import { matchRoutes, normalizePath, ROUTES, splitLocation, type RouteMatch } from './router';

let path = $state(normalizePath(window.location.pathname));
/** The query string without its leading '?'; "" when there is none. */
let search = $state(window.location.search.replace(/^\?/, ''));

/** Current normalized path, its query string, and its route match. */
export const router = {
  get path(): string {
    return path;
  },
  get match(): RouteMatch | null {
    return matchRoutes(ROUTES, path);
  },
  get search(): string {
    return search;
  },
  /**
   * The query string, parsed. A fresh object on every read, so a screen that
   * mutates it cannot corrupt the router's own state — and reading it registers
   * a dependency on `search`, which is what makes a filter change re-run the
   * screen that owns it.
   */
  get params(): URLSearchParams {
    return new URLSearchParams(search);
  },
};

/**
 * Push a new path; replaces the entry when `replace` is set.
 *
 * `to` may carry a query string, which is how the filtered explore scopes keep
 * their state addressable. Scrolling to the top is tied to the PATH changing: a
 * new screen starts at the top, but narrowing a filter on the screen you are
 * already reading must not throw you back to the top of it.
 */
export function navigate(to: string, options: { replace?: boolean } = {}): void {
  const next = splitLocation(to);
  if (next.path === path && next.search === search) return;
  const url = next.search === '' ? next.path : `${next.path}?${next.search}`;
  if (options.replace) {
    window.history.replaceState({}, '', url);
  } else {
    window.history.pushState({}, '', url);
  }
  const moved = next.path !== path;
  path = next.path;
  search = next.search;
  if (moved) window.scrollTo(0, 0);
}

/** True when `href` is the active route (used for nav highlighting). */
export function isActive(href: string, exact = false): boolean {
  const target = splitLocation(href).path;
  return exact ? path === target : path === target || path.startsWith(`${target}/`);
}

function isPlainLeftClick(event: MouseEvent): boolean {
  return (
    event.button === 0 &&
    !event.metaKey &&
    !event.ctrlKey &&
    !event.shiftKey &&
    !event.altKey &&
    !event.defaultPrevented
  );
}

/**
 * Intercept same-origin anchor clicks so ordinary `<a href="/movies">` markup
 * routes client-side while staying a real link for keyboard and middle-click.
 */
export function startRouter(): () => void {
  const onClick = (event: MouseEvent) => {
    if (!isPlainLeftClick(event)) return;
    const anchor = (event.target as Element | null)?.closest('a');
    if (!anchor) return;
    if (anchor.target && anchor.target !== '_self') return;
    if (anchor.hasAttribute('download')) return;
    const href = anchor.getAttribute('href');
    if (!href || href.startsWith('http') || href.startsWith('#')) return;

    const url = new URL(href, window.location.origin);
    if (url.origin !== window.location.origin) return;

    event.preventDefault();
    // The search is carried over: a filtered view's link IS its query string,
    // so dropping it here would make every shared explore URL in the app open
    // the unfiltered screen.
    navigate(`${url.pathname}${url.search}`);
  };

  const onPop = () => {
    path = normalizePath(window.location.pathname);
    search = window.location.search.replace(/^\?/, '');
  };

  document.addEventListener('click', onClick);
  window.addEventListener('popstate', onPop);
  return () => {
    document.removeEventListener('click', onClick);
    window.removeEventListener('popstate', onPop);
  };
}
