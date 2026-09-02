<script lang="ts">
  /**
   * Where a universal-search grab lands.
   *
   * The per-item picker never asks this: it was opened FROM an item, so the
   * item is the answer. A free-text search has no item, so the question has to
   * be asked, and it has exactly two honest answers.
   *
   * "Download only" is the default because it is the one that cannot be wrong:
   * the payload parks in scan review scoped to the chosen library and the user
   * matches it by hand, which is the same graceful-degradation path the scanner
   * already uses (SPEC §13). "Tie to an item" is the better outcome when the
   * item exists — the import knows what it is importing — so it is offered, but
   * it is never guessed at.
   *
   * The library is asked for in both modes because the server requires it in
   * both: an untied grab has nothing else to say where the file belongs.
   */
  import { api, errorText } from '../api/client';
  import type { Library, LibraryKind, Release, SearchGrabRequest } from '../api/types';
  import { useI18n } from '../i18n.svelte';
  import { truncateMiddle } from '../format';
  import { createTypeahead } from '../typeahead.svelte';
  import { libraries } from '../state/libraries.svelte';
  import { pushToast } from '../state/toast.svelte';
  import AddItemModal from './AddItemModal.svelte';
  import Button from './Button.svelte';
  import Modal from './Modal.svelte';
  import TextInput from './TextInput.svelte';

  interface Props {
    release: Release;
    onclose: () => void;
    /** Fired after the server accepted the grab; the screen decides what next. */
    ongrabbed?: () => void;
  }

  let { release, onclose, ongrabbed }: Props = $props();
  const { t } = useI18n();

  /** One candidate for a tie: the three item kinds reduced to what a tie needs. */
  interface TieItem {
    id: number;
    title: string;
    /** 0 means "the kind's default library", exactly as it does server-side. */
    library_id: number;
    /**
     * Which endpoint the tie names. It travels on the row rather than being
     * read off the library, because an anime library holds films AND series —
     * so the library alone cannot say, and only the chosen row can.
     */
    media_type: 'movie' | 'series';
  }

  let mode = $state<'park' | 'tie'>('park');
  let libraryID = $state(0);
  let tied = $state<TieItem | null>(null);
  /** Optional narrowing for a series tie; '' is "the whole series". */
  let season = $state('');
  let episode = $state('');
  let busy = $state(false);
  let adding = $state(false);

  // /libraries is admin-only, so the store is loaded when a picker opens
  // rather than at startup. The server already omits libraries this account
  // may not see.
  $effect(() => void libraries.load());

  // Dormant shelves are the one thing that list still carries and this one may
  // not offer: a grab named onto one is a 404, tie or no tie, because an
  // inactive library refuses everyone (core.LibraryVisible). `targetable()`
  // states that once, for every picker.
  let choices = $derived(libraries.targetable());
  // The first library is the opening answer rather than a blank: a select with
  // no valid value would make Confirm fail on a question the user never saw.
  $effect(() => {
    if (libraryID !== 0 && choices.some((l) => l.id === libraryID)) return;
    libraryID = choices[0]?.id ?? 0;
  });

  let library = $derived<Library | undefined>(choices.find((l) => l.id === libraryID));
  let kind = $derived<LibraryKind>(library?.kind ?? 'movie');
  /**
   * Which endpoint the tie names.
   *
   * The chosen row answers for itself where there is one, because an anime
   * library offers both. Without one the library's own vocabulary stands in —
   * and an adult site is a series row, which is why a tie to one is a series
   * tie. Only the season inputs and the metadata add read this before a row is
   * picked; `confirm` never does.
   */
  let tieMediaType = $derived<'movie' | 'series'>(
    tied?.media_type ?? (kind === 'movie' ? 'movie' : 'series'),
  );
  let seasonsApply = $derived(tieMediaType === 'series');
  /**
   * The scope the "add from metadata" dialog opens on, or null for "let the
   * user choose". An anime library accepts a film and a series alike, so
   * fixing the tab there would hide half of what the shelf holds.
   */
  let addScope = $derived<'movie' | 'series' | null>(kind === 'anime' ? null : tieMediaType);

  /**
   * The library's own items, fetched once per kind. A tie must name an item the
   * server agrees belongs to the chosen library, so the filter below mirrors
   * itemInLibrary in internal/api/searchreleases.go exactly — including that a
   * library_id of 0 belongs to its kind's default library.
   */
  let items = $state<TieItem[]>([]);
  let itemsKind = $state<LibraryKind | null>(null);
  let itemsError = $state<string | null>(null);

  $effect(() => {
    if (mode !== 'tie') return;
    const wanted = kind;
    if (itemsKind === wanted) return;
    void loadItems(wanted);
  });

  function movieTie(m: { id: number; title: string; library_id: number }): TieItem {
    return { id: m.id, title: m.title, library_id: m.library_id, media_type: 'movie' };
  }

  function seriesTie(s: { id: number; title: string; library_id: number }): TieItem {
    return { id: s.id, title: s.title, library_id: s.library_id, media_type: 'series' };
  }

  async function loadItems(wanted: LibraryKind) {
    try {
      let rows: TieItem[];
      if (wanted === 'movie') {
        rows = (await api.listMovies()).map(movieTie);
      } else if (wanted === 'tv') {
        rows = (await api.listSeries()).map(seriesTie);
      } else if (wanted === 'anime') {
        // Both tables: an anime library owns films and series together, and a
        // release parked against it may be either.
        const [films, shows] = await Promise.all([
          api.listMovies(),
          api.listSeries({ kind: 'anime' }),
        ]);
        rows = [...films.map(movieTie), ...shows.map(seriesTie)];
      } else {
        rows = (await api.listSites()).map(seriesTie);
      }
      items = rows;
      itemsKind = wanted;
      itemsError = null;
    } catch (err) {
      items = [];
      itemsError = errorText(err);
    }
  }

  function inChosenLibrary(item: TieItem): boolean {
    if (!library) return false;
    if (item.library_id !== 0) return item.library_id === library.id;
    return library.is_default;
  }

  // The same typeahead every other picker uses. The search is local — the list
  // is already here — but the debounce, the minimum length and the "nothing to
  // search for yet" state are what make it behave like the others, and a
  // library with a few thousand titles is exactly where a debounce earns its
  // keep. A blank query lists the library so the box opens with something.
  const search = createTypeahead<TieItem[]>({
    blank: () => [],
    searchBlank: true,
    run: async (q) => {
      const needle = q.toLowerCase();
      return items
        .filter(inChosenLibrary)
        .filter((item) => needle === '' || item.title.toLowerCase().includes(needle))
        .slice(0, 20);
    },
    // `run` is called from a timer, so what it reads there is nobody's
    // dependency; the list and the chosen library are declared here instead.
    depends: () => `${libraryID}:${items.length}`,
  });

  // Changing the library invalidates a tie made against the previous one: the
  // server would refuse it ("does not belong to that library"), and silently
  // sending a doomed request is worse than clearing the field in front of the
  // user.
  $effect(() => {
    void libraryID;
    tied = null;
  });

  /**
   * Adopt an item the metadata dialog just created.
   *
   * The list is refetched rather than the row being synthesised, because this
   * dialog's own list is what `confirm` ties against — a synthesised row would
   * be a tie to something the list does not contain.
   *
   * The add IS told where to land (`libraryID` above), so landing elsewhere is
   * no longer the ordinary case. The check below stays anyway: the server owns
   * where a row ends up, and reading it back is what turns a disagreement into
   * a sentence here rather than a confusing 400 at Confirm.
   */
  async function onAdded(addedKind: 'movie' | 'series', item: { id: number; title: string }) {
    adding = false;
    await loadItems(kind);
    // Matched on the pair, not the id: a film and a series may both be row 12,
    // and on an anime shelf both are in this list at once.
    const row = items.find(
      (candidate) => candidate.id === item.id && candidate.media_type === addedKind,
    );
    if (row && inChosenLibrary(row)) {
      tied = row;
      return;
    }
    pushToast(
      t('component.grabTarget.addedElsewhere', { title: item.title }),
      'warning',
    );
  }

  function optionalNumber(raw: string): number | undefined {
    const trimmed = raw.trim();
    if (trimmed === '') return undefined;
    const n = Number(trimmed);
    return Number.isInteger(n) && n >= 0 ? n : undefined;
  }

  let canConfirm = $derived(
    !busy && libraryID > 0 && (mode === 'park' || tied !== null),
  );

  async function confirm() {
    if (!canConfirm) return;
    busy = true;
    try {
      const body: SearchGrabRequest = { release_id: release.id, library_id: libraryID };
      if (mode === 'tie' && tied) {
        body.tie = {
          media_type: tieMediaType,
          media_id: tied.id,
          ...(seasonsApply ? optionalScope() : {}),
        };
      }
      await api.grabFromSearch(body);
      pushToast(t('component.grabTarget.grabbed', { title: release.title }), 'success');
      ongrabbed?.();
      onclose();
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busy = false;
    }
  }

  function optionalScope(): { season?: number; episode?: number } {
    const s = optionalNumber(season);
    const e = optionalNumber(episode);
    return { ...(s !== undefined ? { season: s } : {}), ...(e !== undefined ? { episode: e } : {}) };
  }

  let libraryName = $derived(library?.name ?? t('component.grabTarget.chosenLibrary'));
</script>

<Modal title={t('component.grabTarget.title')} width="max-w-xl" {onclose}>
  <div class="flex flex-col gap-4 p-4">
    <p class="break-words font-mono text-sm text-ink-secondary" title={release.title}>
      {truncateMiddle(release.title, 72)}
    </p>

    <label class="flex items-center gap-3 rounded-md border border-border bg-raised px-3 py-2">
      <span class="text-base text-ink">{t('component.grabTarget.library')}</span>
      <select
        bind:value={libraryID}
        class="h-8 flex-1 rounded-sm border border-border-strong bg-raised px-2 text-md text-ink focus:border-accent focus:outline-none"
        aria-label={t('component.grabTarget.targetLibrary')}>
        {#each choices as choice (choice.id)}
          <option value={choice.id}>{choice.name}</option>
        {/each}
      </select>
    </label>

    <div class="flex flex-col gap-2" role="radiogroup" aria-label={t('component.grabTarget.action')}>
      <label class="flex items-start gap-3 rounded-md border border-border bg-raised px-3 py-2">
        <input
          type="radio"
          name="grab-mode"
          value="park"
          checked={mode === 'park'}
          onchange={() => (mode = 'park')}
          class="mt-1 size-4 accent-accent" />
        <span class="min-w-0">
          <span class="block text-base text-ink">{t('component.grabTarget.downloadOnly')}</span>
          <span class="block text-sm text-ink-secondary">
            {t('component.grabTarget.downloadOnlyHelp', { library: libraryName })}
          </span>
        </span>
      </label>

      <label class="flex items-start gap-3 rounded-md border border-border bg-raised px-3 py-2">
        <input
          type="radio"
          name="grab-mode"
          value="tie"
          checked={mode === 'tie'}
          onchange={() => (mode = 'tie')}
          class="mt-1 size-4 accent-accent" />
        <span class="min-w-0">
          <span class="block text-base text-ink">{t('component.grabTarget.tie')}</span>
          <span class="block text-sm text-ink-secondary">
            {t('component.grabTarget.tieHelp')}
          </span>
        </span>
      </label>
    </div>

    {#if mode === 'tie'}
      <div data-tie-picker class="flex flex-col gap-2">
        {#if tied}
          <div class="flex items-center gap-2 rounded-md border border-accent bg-accent-tint px-3 py-2">
            <span class="min-w-0 flex-1 truncate text-base text-accent-text" title={tied.title}>
              {tied.title}
            </span>
            <Button variant="ghost" size="sm" onclick={() => (tied = null)}>{t('component.grabTarget.change')}</Button>
          </div>
        {:else}
          <TextInput
            bind:value={search.query}
            type="search"
            placeholder={t('component.grabTarget.findPlaceholder', { library: libraryName })}
            ariaLabel={t('component.grabTarget.findAria')} />
          {#if itemsError}
            <p class="text-sm text-danger">{itemsError}</p>
          {:else if search.results.length === 0}
            <p class="text-sm text-ink-muted">
              {t('component.grabTarget.noMatch', { library: libraryName })}
            </p>
          {:else}
            <ul class="flex max-h-48 flex-col overflow-y-auto rounded-md border border-border">
              {#each search.results as item (`${item.media_type}:${item.id}`)}
                <li>
                  <button
                    type="button"
                    onclick={() => (tied = item)}
                    class="flex w-full items-center px-3 py-2 text-left text-base text-ink transition-colors duration-150 ease-out hover:bg-raised">
                    <span class="min-w-0 truncate" title={item.title}>{item.title}</span>
                  </button>
                </li>
              {/each}
            </ul>
          {/if}

          {#if kind !== 'adult'}
            <!-- Adult sites are added by stash-box id, not a TMDB id, so the
                 metadata add this opens has nothing to offer that scope. -->
            <Button variant="secondary" onclick={() => (adding = true)}>
              {t('component.grabTarget.addFromMetadata')}
            </Button>
          {/if}
        {/if}

        {#if seasonsApply && tied}
          <!-- Optional on purpose: leaving both blank grabs for the whole
               series, which is what a season pack usually is. -->
          <div class="flex flex-wrap items-center gap-2">
            <label class="flex items-center gap-2 text-sm text-ink-secondary">
              {t('component.grabTarget.season')}
              <!-- Read as text, not `bind:value` on a number input: a blank
                   box means "the whole series", and a numeric binding turns
                   that into a value nobody typed. -->
              <input
                type="number"
                min="0"
                value={season}
                oninput={(event) => (season = event.currentTarget.value)}
                aria-label={t('component.grabTarget.seasonNumber')}
                class="h-8 w-20 rounded-sm border border-border-strong bg-raised px-2 text-md text-ink focus:border-accent focus:outline-none" />
            </label>
            <label class="flex items-center gap-2 text-sm text-ink-secondary">
              {t('component.grabTarget.episode')}
              <input
                type="number"
                min="0"
                value={episode}
                oninput={(event) => (episode = event.currentTarget.value)}
                aria-label={t('component.grabTarget.episodeNumber')}
                class="h-8 w-20 rounded-sm border border-border-strong bg-raised px-2 text-md text-ink focus:border-accent focus:outline-none" />
            </label>
            <span class="text-sm text-ink-muted">{t('component.grabTarget.wholeSeries')}</span>
          </div>
        {/if}
      </div>
    {/if}
  </div>

  {#snippet footer()}
    <Button variant="ghost" disabled={busy} onclick={onclose}>{t('component.grabTarget.cancel')}</Button>
    <Button variant="primary" disabled={!canConfirm} onclick={confirm}>
      {busy ? t('component.grabTarget.grabbing') : t('component.grabTarget.grab')}
    </Button>
  {/snippet}
</Modal>

{#if adding}
  <!-- Both the library and the kind are fixed to this tie's, so the add cannot
       land somewhere the tie would then be refused for. The kind is the one
       exception on an anime shelf, which holds films and series alike — there
       the dialog shows both and each row says which it is. `onadded` is what
       keeps the user here: without it the add navigates to the new item's page
       and abandons the release they came in with. -->
  <AddItemModal
    title={t('component.grabTarget.addThenTie')}
    kind={addScope}
    {libraryID}
    onadded={onAdded}
    onclose={() => (adding = false)} />
{/if}
