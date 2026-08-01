/**
 * Reactive router state over the History API. The server serves the SPA with a
 * fallback to index.html, so real paths work on reload.
 */

import { matchRoutes, normalizePath, ROUTES, type RouteMatch } from './router';

let path = $state(normalizePath(window.location.pathname));

/** Current normalized path and its route match. */
export const router = {
  get path(): string {
    return path;
  },
  get match(): RouteMatch | null {
    return matchRoutes(ROUTES, path);
  },
};

/** Push a new path; replaces the entry when `replace` is set. */
export function navigate(to: string, options: { replace?: boolean } = {}): void {
  const next = normalizePath(to);
  if (next === path) return;
  if (options.replace) {
    window.history.replaceState({}, '', next);
  } else {
    window.history.pushState({}, '', next);
  }
  path = next;
  window.scrollTo(0, 0);
}

/** True when `href` is the active route (used for nav highlighting). */
export function isActive(href: string, exact = false): boolean {
  const target = normalizePath(href);
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
    navigate(url.pathname);
  };

  const onPop = () => {
    path = normalizePath(window.location.pathname);
  };

  document.addEventListener('click', onClick);
  window.addEventListener('popstate', onPop);
  return () => {
    document.removeEventListener('click', onClick);
    window.removeEventListener('popstate', onPop);
  };
}
