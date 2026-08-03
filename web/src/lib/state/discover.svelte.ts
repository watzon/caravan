/**
 * The discover landing page's payload (SPEC §11 `GET /discover`).
 *
 * It is a store rather than route-local state for one reason: the endpoint
 * costs three sequential TMDB round trips, so bouncing into a title and back
 * must not pay for them twice. The cache is kept honest by the two mark*
 * methods, which patch what the user just did into every shelf holding that
 * title instead of refetching the whole page for one changed flag.
 */

import { ApiError, api, errorText } from '../api/client';
import type { DiscoverHome, DiscoverItem, MediaType } from '../api/types';

class DiscoverState {
  home = $state<DiscoverHome | null>(null);
  error = $state<string | null>(null);
  loading = $state(false);
  /** The ApiError status of the last failure: 503 = no key, 502 = provider. */
  status = $state(0);

  #inFlight = false;

  /**
   * Fetch the landing page once. A cached payload is reused unless `force` is
   * set (the retry button, and a manual refresh).
   */
  async load(force = false): Promise<void> {
    if (this.#inFlight) return;
    if (this.home !== null && !force) return;
    this.#inFlight = true;
    this.loading = true;
    try {
      this.home = await api.discoverHome();
      this.error = null;
      this.status = 0;
    } catch (err) {
      this.error = errorText(err);
      this.status = err instanceof ApiError ? err.status : 0;
    } finally {
      this.#inFlight = false;
      this.loading = false;
    }
  }

  /** A request was just recorded: every shelf showing that title says so. */
  markRequested(mediaType: MediaType, tmdbID: number): void {
    this.#patch(mediaType, tmdbID, (item) => {
      item.requested = true;
    });
  }

  /**
   * A title just entered the library. Its pending request was absorbed
   * server-side, so `requested` drops with the same edit.
   */
  markInLibrary(mediaType: MediaType, tmdbID: number, libraryID: number): void {
    this.#patch(mediaType, tmdbID, (item) => {
      item.in_library = true;
      item.library_id = libraryID;
      item.requested = false;
    });
  }

  /** Drop the cache — used by tests, and by a hard refresh of the screen. */
  reset(): void {
    this.home = null;
    this.error = null;
    this.status = 0;
    this.loading = false;
  }

  #patch(mediaType: MediaType, tmdbID: number, apply: (item: DiscoverItem) => void): void {
    const home = this.home;
    if (!home) return;
    for (const shelf of [home.trending, home.popular_movies, home.popular_series]) {
      for (const item of shelf) {
        if (item.media_type === mediaType && item.tmdb_id === tmdbID) apply(item);
      }
    }
  }
}

export const discover = new DiscoverState();
