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
import { libraryKindAccepts } from '../library';

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

  /**
   * The shelves anything may be filed onto: the ACTIVE ones.
   *
   * GET /libraries deliberately sends dormant rows too — it is the management
   * surface, and the toggle that revives a shelf has to stay reachable — but a
   * dormant library refuses EVERYONE, admins included (core.LibraryVisible),
   * so every content route answers 404 for one: the search, the add, the move
   * and the grab alike. A target the server will refuse is not a target, so it
   * is filtered here, once, rather than in each picker that would otherwise
   * offer a shelf and then fail on it.
   */
  targetable(): Library[] {
    return this.all.filter((l) => l.active);
  }

  /**
   * The libraries an item of this vocabulary may land in, defaults first.
   *
   * Acceptance rather than equality of kind: an anime library holds films and
   * series together, so a movie add or move may target a movie library OR an
   * anime one, and a series either a television or an anime one.
   * `libraryKindAccepts` mirrors the rule the server's add and move endpoints
   * enforce, so every picker offers exactly the targets that will be accepted —
   * one statement of the rule rather than a ternary per dialog.
   *
   * Defaults first because an untargeted add lands on the default, so the
   * select's opening answer and the server's are the same library.
   */
  accepting(itemKind: LibraryKind): Library[] {
    return this.targetable()
      .filter((l) => libraryKindAccepts(l.kind, itemKind))
      .sort((a, b) => Number(b.is_default) - Number(a.is_default) || a.id - b.id);
  }
}

export const libraries = new LibrariesState();
