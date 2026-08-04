/**
 * Search-as-you-type state: one query field, a debounce, a minimum length, and
 * exactly one request's answer on screen at a time.
 *
 * It exists because the ⌘K add flow and the add-a-site picker are the same
 * interaction over two different providers, and the parts worth getting right
 * are the invisible ones — cancelling the request a newer keystroke has already
 * outdated, not showing the loser's error, not asking a provider about a single
 * character. Those are what drift when a screen grows its own copy.
 *
 * What it deliberately does NOT own is what a result looks like. The two
 * callers render different rows with different copy against different id types,
 * and a component that took both would be a switch on which caller it is.
 */

import { errorText } from './api/client';
import { DEBOUNCE_MS, MIN_QUERY } from './typeahead';

export interface TypeaheadOptions<T> {
  /**
   * Runs one search. The signal is aborted as soon as a newer keystroke lands,
   * so a slow answer cannot overwrite a fresh one.
   */
  run: (query: string, signal: AbortSignal) => Promise<T>;
  /** The results when there is nothing to search for. */
  blank: () => T;
  /** Seeds the field; the manual-match flow opens with the parsed title in it. */
  initial?: string;
  minQuery?: number;
  debounceMs?: number;
  /**
   * Treat a blank query as a search of its own rather than as nothing to do.
   * A stash-box endpoint answers one with its own default list, which is a
   * better opening state than a hint telling the user to type; TMDB has nothing
   * to say about an empty query.
   *
   * It takes a function as well as a flag because a dialog that searches two
   * providers answers this per scope, and the answer has to be read when the
   * question is asked rather than fixed when the typeahead was built.
   */
  searchBlank?: boolean | (() => boolean);
  /**
   * Anything else the search depends on, read on every pass. A scope the caller
   * flips — the Movies/Series tabs — is read through here rather than captured
   * in `run`, because `run` is called from a timer and a read from inside a
   * timer is not a dependency of anything.
   */
  depends?: () => unknown;
}

export interface Typeahead<T> {
  /** Bound to the search field. */
  query: string;
  /** The query as it is actually searched; what "no matches" should quote. */
  readonly trimmed: string;
  readonly results: T;
  readonly loading: boolean;
  readonly error: string | null;
  /** True when there is nothing to search for, so the caller can say why. */
  readonly idle: boolean;
  /** Run the current query again — the Retry on a failed search. */
  retry(): void;
}

/**
 * Create a typeahead. It must be called while a component is initialising (the
 * top of a `<script>`), because it owns an `$effect`; its searching then stops
 * when that component goes away, which is the lifetime a modal wants.
 */
export function createTypeahead<T>(options: TypeaheadOptions<T>): Typeahead<T> {
  const {
    run,
    blank,
    initial = '',
    minQuery = MIN_QUERY,
    debounceMs = DEBOUNCE_MS,
    searchBlank = false,
    depends,
  } = options;

  let query = $state(initial);
  let results = $state<T>(blank());
  let loading = $state(false);
  let error = $state<string | null>(null);
  // Bumped by retry(). It is state rather than a direct call so a retry goes
  // through the one path a keystroke does, debounce and cancellation included.
  let attempt = $state(0);

  function searchable(q: string): boolean {
    if (q !== '') return q.length >= minQuery;
    return typeof searchBlank === 'function' ? searchBlank() : searchBlank;
  }

  $effect(() => {
    const q = query.trim();
    // Read before the early return, or a scope change while the query is too
    // short would never restart the search.
    void attempt;
    depends?.();

    if (!searchable(q)) {
      results = blank();
      loading = false;
      error = null;
      return;
    }

    const controller = new AbortController();
    // Set before the wait, not after it: the waiting state belongs to the
    // keystroke, not to the request it eventually becomes.
    loading = true;
    const timer = setTimeout(async () => {
      try {
        results = await run(q, controller.signal);
        error = null;
      } catch (err) {
        // A request that lost the race is not a failure anybody should see:
        // the newer one owns the screen now.
        if (controller.signal.aborted) return;
        error = errorText(err);
      } finally {
        if (!controller.signal.aborted) loading = false;
      }
    }, debounceMs);

    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  });

  return {
    get query() {
      return query;
    },
    set query(next: string) {
      query = next;
    },
    get trimmed() {
      return query.trim();
    },
    get results() {
      return results;
    },
    get loading() {
      return loading;
    },
    get error() {
      return error;
    },
    get idle() {
      return !searchable(query.trim());
    },
    retry() {
      attempt += 1;
    },
  };
}
