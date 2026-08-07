/**
 * The library list for pickers: the add dialog's target select, and every
 * later surface that offers "which library" as a choice.
 *
 * It is a store rather than modal-local state so the list is fetched once per
 * session however many pickers open. GET /libraries is admin-only, so load()
 * is called by admin-facing surfaces when they open — never eagerly at
 * startup — and a member session makes no request.
 */

import { api } from '../api/client';
import type { Library, LibraryKind } from '../api/types';

class LibrariesState {
  all = $state<Library[]>([]);
  loaded = $state(false);

  #inFlight = false;

  /** Fetch the list once. `force` refetches (a library was just created). */
  async load(force = false): Promise<void> {
    if (this.#inFlight) return;
    if (this.loaded && !force) return;
    this.#inFlight = true;
    try {
      this.all = await api.listLibraries();
      this.loaded = true;
    } catch {
      // A picker that cannot load the list simply offers no choice; an add
      // without a target still lands in the default library server-side.
      this.all = [];
    } finally {
      this.#inFlight = false;
    }
  }

  /** The libraries an item of this kind may land in, defaults first. */
  ofKind(kind: LibraryKind): Library[] {
    return this.all
      .filter((l) => l.kind === kind)
      .sort((a, b) => Number(b.is_default) - Number(a.is_default) || a.id - b.id);
  }
}

export const libraries = new LibrariesState();
