/**
 * Reactive router state over the History API. The server serves the SPA with a
 * fallback to index.html, so real paths work on reload.
 */

import { matchRoutes, normalizePath, ROUTES, splitLocation, type RouteMatch } from './router';

let path = $state(normalizePath(window.location.pathname));
/** The query string without its leading '?'; "" when there is none. */
let search = $state(window.location.search.replace(/^\?/, ''));
/** The fragment including its leading '#'; "" when there is none. */
let hash = $state(window.location.hash);

/** Current normalized path, query string, fragment, and route match. */
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
  get hash(): string {
    return hash;
  },
  /**
   * The query string, parsed. A fresh object on every read, so a screen that
   * mutates it cannot corrupt the router's own state. Reading it registers a
   * dependency on `search`, which is what makes a filter change re-run the
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
 * their state addressable. A fragment is kept in the History URL and scrolled
 * into view after Svelte has rendered the destination. Scrolling to the top is
 * tied to a path change only when there is no fragment: an anchor names the
 * position the user asked to see.
 */
export function navigate(to: string, options: { replace?: boolean } = {}): void {
  const next = splitLocation(to);
  if (next.path === path && next.search === search && next.hash === hash) return;
  const url = `${next.path}${next.search === '' ? '' : `?${next.search}`}${next.hash}`;
  if (options.replace) {
    window.history.replaceState({}, '', url);
  } else {
    window.history.pushState({}, '', url);
  }
  const moved = next.path !== path;
  path = next.path;
  search = next.search;
  hash = next.hash;
  if (hash) {
    queueMicrotask(() => {
      document.getElementById(hash.slice(1))?.scrollIntoView?.();
    });
  } else if (moved) {
    window.scrollTo(0, 0);
  }
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
 * Downloads and API URLs must remain document navigations so the browser can
 * receive the response body rather than asking the SPA to render it.
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
    if (url.pathname === '/api' || url.pathname.startsWith('/api/')) return;

    event.preventDefault();
    // The search and fragment are carried over: a filtered view's link IS its
    // query string, and a settings guide's link names its exact section.
    navigate(`${url.pathname}${url.search}${url.hash}`);
  };

  const onPop = () => {
    path = normalizePath(window.location.pathname);
    search = window.location.search.replace(/^\?/, '');
    hash = window.location.hash;
  };

  document.addEventListener('click', onClick);
  window.addEventListener('popstate', onPop);
  return () => {
    document.removeEventListener('click', onClick);
    window.removeEventListener('popstate', onPop);
  };
}
