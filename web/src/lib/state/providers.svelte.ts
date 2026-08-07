/**
 * The compiled-in metadata providers, for surfaces that must NAME one rather
 * than choose one: the add dialog's per-row badge when a library's chain is
 * longer than a single provider.
 *
 * It is a store for libraries.svelte.ts's reason — the list is the same for
 * every surface and changes only when Caravan is rebuilt, so it is fetched
 * once per session however many dialogs open. GET /libraries/providers is
 * admin-only, so load() is called lazily by the surface that needs a name, and
 * a member session simply gets no names: `name()` falls back to the raw id,
 * which is a worse label but never a wrong one.
 */

import { api } from '../api/client';
import type { MetadataProviderInfo } from '../api/types';

class ProvidersState {
  all = $state<MetadataProviderInfo[]>([]);
  loaded = $state(false);

  #inFlight = false;

  /** Fetch the list once. `force` refetches. */
  async load(force = false): Promise<void> {
    if (this.#inFlight) return;
    if (this.loaded && !force) return;
    this.#inFlight = true;
    try {
      this.all = await api.listMetadataProviders();
      this.loaded = true;
    } catch {
      // A badge that cannot be named still says which provider answered, by
      // id. Degrading to that is better than hiding the row's origin.
      this.all = [];
    } finally {
      this.#inFlight = false;
    }
  }

  /** This provider's display name, or the raw id when it is not (yet) known. */
  name(id: string): string {
    return this.all.find((p) => p.id === id)?.name ?? id;
  }
}

export const providers = new ProvidersState();
