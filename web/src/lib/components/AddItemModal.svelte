<script lang="ts">
  /**
   * The ⌘K add flow (SPEC §9 steps 1-2): search, then "add to library" with
   * monitoring on. Also reusable as a plain picker for the scan-review manual
   * match, where `onpick` replaces the add call.
   *
   * Two providers, one dialog. Movies and series come from TMDB; the Adult
   * scope searches the stash-box endpoint and adds a site. The scope only
   * exists where the module does — `session.adult`, the same single boolean the
   * sidebar row reads — and a caller cannot select it into being: see `scope`.
   * With it absent, nothing here touches an adult type or an adult route.
   */
  import { untrack } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { MovieMeta, SearchResults, SeriesMeta, SiteMeta } from '../api/types';
  import { siteHref } from '../adult';
  import { ratingPresentation } from '../discover';
  import { isFuture } from '../format';
  import { metadataFault, type CredentialFault } from '../credentials';
  import { navigate } from '../router.svelte';
  import { libraries } from '../state/libraries.svelte';
  import { session } from '../state/session.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { focusFirstResult, moveResultFocus } from '../typeahead';
  import { createTypeahead } from '../typeahead.svelte';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import CredentialEmptyState from './CredentialEmptyState.svelte';
  import EmptyState from './EmptyState.svelte';
  import LoadError from './LoadError.svelte';
  import Modal from './Modal.svelte';
  import Poster from './Poster.svelte';
  import SearchSkeleton from './SearchSkeleton.svelte';
  import TextInput from './TextInput.svelte';

  /** What the dialog is searching. */
  type Scope = 'movie' | 'series' | 'site';

  /**
   * The scopes a pick-mode caller can be handed back.
   *
   * Sites are not among them, and that is a statement about the flow rather
   * than a gap: pick mode exists for the scan-review manual match, which points
   * a file on disk at a TMDB id. A site is identified by a stash-box id, a
   * string — so rather than widen `tmdbID` into something that means two things
   * depending on a sibling argument, pick mode simply has no adult scope.
   */
  type PickKind = 'movie' | 'series';
  interface AddTarget {
    kind: PickKind;
    row: MovieMeta | SeriesMeta;
    releaseDate: string;
  }

  interface Props {
    onclose: () => void;
    /** Restrict the picker to one scope (scan review knows what it parsed). */
    kind?: Scope | null;
    /** Which tab starts selected when several scopes are available. */
    initialKind?: Scope;
    /** Prefill for the manual-match flow. */
    initialQuery?: string;
    title?: string;
    /**
     * When supplied the modal picks instead of adding: the caller decides what
     * to do with the chosen TMDB id. Its presence also removes the adult
     * scope — see PickKind.
     */
    onpick?: (kind: PickKind, tmdbID: number) => Promise<void> | void;
    /**
     * When supplied the add still happens, but the caller keeps the user
     * instead of the router: the created item is handed back and the modal
     * neither closes itself nor navigates to the new item's page.
     *
     * The grab-target dialog is what needs it — "add this title, then tie this
     * release to it" is one flow, and navigating away mid-flow would abandon
     * the release the user came in with. Everything else about the add is
     * unchanged, monitoring and search-on-add included.
     *
     * Sites are deliberately out of scope, exactly as they are for `onpick`:
     * the tie a caller builds from this names a movie or a series.
     */
    onadded?: (kind: PickKind, item: { id: number; title: string }) => void;
  }

  let {
    onclose,
    kind: fixedKind = null,
    initialKind = 'movie',
    initialQuery = '',
    title = 'Add to library',
    onpick,
    onadded,
  }: Props = $props();

  /**
   * The two add options, both OFF by default and both deliberately per-add
   * rather than sticky.
   *
   * Adding something is now cheap and reversible; committing the server to
   * following it, and to searching every indexer for it right away, is neither.
   * So the safe answer is the default one, and it is re-chosen each time the
   * dialog opens instead of being remembered — a habit that quietly monitors
   * everything is the failure mode this replaces.
   *
   * Searching is nested under monitoring because it is meaningless without it:
   * the wanted list is what a search reads, and nothing unmonitored is on it.
   * The server agrees rather than trusting this (search_now on an unmonitored
   * add queues nothing), but offering the combination would be offering a
   * button that does nothing.
   */
  let monitorOnAdd = $state(false);
  let searchOnAdd = $state(false);

  function setMonitorOnAdd(next: boolean) {
    monitorOnAdd = next;
    // Unchecking hides the search box, so its value has to go with it:
    // a hidden checkbox that is still true would search on the next add
    // for a reason nothing on screen explains.
    if (!next) searchOnAdd = false;
  }

  // Both props seed local state once: the modal is remounted per use, so
  // reading them untracked is the intent, not an oversight.
  let kind = $state<Scope>(untrack(() => fixedKind ?? initialKind));
  let busyID = $state<number | null>(null);
  /** The site whose add is in flight; sites are named by a string, not an id. */
  let busyStashID = $state<string | null>(null);
  /** A future- or unknown-release title waiting for the user's explicit approval. */
  let confirmingRelease = $state<AddTarget | null>(null);

  /**
   * Whether the adult scope is on screen. `session.adult` is the server's own
   * combined answer (module on AND this account reaches it), the same one the
   * sidebar row reads, and an unknown identity reads as false.
   *
   * Pick mode drops it too: a manual match is a TMDB id by definition, and a
   * hand-back caller (`onadded`) builds a tie, which names a movie or a
   * series. Neither could do anything with a site, so neither is offered one.
   */
  let siteScope = $derived(!onpick && !onadded && session.adult);

  // Add mode offers a target library when the scope's kind has more than one.
  // Pick mode adds nothing, so it neither needs the list nor may fetch it —
  // and the fetch is lazy for the store's reason: /libraries is admin-only.
  $effect(() => {
    if (!onpick) void libraries.load();
  });
  /** The chosen target library; 0 is "the default", which needs no request field. */
  let targetLibraryID = $state(0);
  let libraryChoices = $derived(
    onpick
      ? []
      : libraries.ofKind(scope === 'movie' ? 'movie' : scope === 'series' ? 'tv' : 'adult'),
  );
  $effect(() => {
    // Scope-dependent state, like the search results: flipping the tab points
    // the select back at that kind's default.
    void scope;
    targetLibraryID = 0;
  });

  let scopes = $derived<{ key: Scope; label: string }[]>([
    { key: 'movie', label: 'Movies' },
    { key: 'series', label: 'Series' },
    ...(siteScope ? [{ key: 'site' as Scope, label: 'Adult' }] : []),
  ]);

  /**
   * The scope actually in force. A caller that asks for `site` on a browser the
   * module is invisible to gets Movies instead — the gate is here rather than at
   * the call sites so there is exactly one of it, and so no path can reach an
   * adult request by passing a prop.
   */
  let scope = $derived<Scope>(kind === 'site' && !siteScope ? 'movie' : kind);

  // Debounce, minimum length and latest-wins cancellation live in the shared
  // typeahead; only the request and the shape of an empty answer differ by
  // scope.
  const search = createTypeahead<SearchResults & { sites: SiteMeta[] }>({
    initial: untrack(() => initialQuery),
    blank: () => ({ movies: [], series: [], sites: [] }),
    run: async (q, signal) => {
      if (scope === 'site') {
        return { movies: [], series: [], sites: await api.searchSites(q, signal) };
      }
      const found = await api.search(q, scope, signal);
      return { movies: found.movies ?? [], series: found.series ?? [], sites: [] };
    },
    // The scope is part of the search: flipping the tab re-runs it.
    depends: () => scope,
  });

  let rows = $derived<(MovieMeta | SeriesMeta)[]>(
    scope === 'movie' ? search.results.movies : search.results.series,
  );
  let sites = $derived<SiteMeta[]>(search.results.sites);

  /**
   * The TMDB credential fault behind the last failure, from whichever half of
   * the dialog hit it (PLAN phase 10 task 3).
   *
   * Both halves need it: the search says "no key" before a row can exist, and
   * the add says it for a key that was revoked between the search and the
   * click. A toast would be the wrong shape for either — the dialog cannot do
   * its job at all until the key is fixed, so it says so where the results
   * would have been, with the destination attached.
   *
   * `addFault` is cleared whenever the query changes, so correcting the key in
   * another tab and searching again is not stuck behind a stale answer.
   */
  let addFault = $state<CredentialFault | null>(null);
  let searchFault = $derived(metadataFault(search.cause));
  // Scoped as a whole, not per-source. TMDB is not what the Adult scope calls,
  // so neither half's fault says anything about it — and scoping only the
  // search half let a failed movie add blank out working stash-box results on
  // the very next tab press, behind an empty state pointing at a settings
  // screen with nothing to do with the failure.
  let credentialFault = $derived<CredentialFault | null>(
    scope === 'site' ? null : (searchFault ?? addFault),
  );

  // A new query is a new attempt: the fault an add reported belongs to the
  // click that caused it, not to the dialog for as long as it is open.
  $effect(() => {
    void search.trimmed;
    addFault = null;
  });

  let body = $state<HTMLElement | null>(null);

  // Palette-style keys while typing: Tab cycles the scope (so it is not focus
  // navigation here), ArrowDown jumps to the first result's button, which is
  // where Tab would otherwise have gone.
  function onSearchKeydown(event: KeyboardEvent) {
    if (event.key === 'Tab' && !event.shiftKey && !fixedKind) {
      event.preventDefault();
      const keys = scopes.map((s) => s.key);
      kind = keys[(keys.indexOf(scope) + 1) % keys.length] ?? 'movie';
      return;
    }
    if (event.key === 'ArrowDown' && focusFirstResult(body)) {
      event.preventDefault();
    }
  }

  // Up/Down walk the result buttons; Up from the first hands focus back to
  // the search field so the whole list is reachable without leaving arrows.
  function onListKeydown(event: KeyboardEvent) {
    moveResultFocus(event, body);
  }

  function hasKnownReleaseDate(value: string): boolean {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
    const date = new Date(`${value}T00:00:00Z`);
    return !Number.isNaN(date.getTime()) && date.toISOString().slice(0, 10) === value;
  }

  function select(row: MovieMeta | SeriesMeta) {
    const targetKind: PickKind = scope === 'series' ? 'series' : 'movie';
    const target: AddTarget = {
      kind: targetKind,
      row,
      releaseDate:
        targetKind === 'movie'
          ? (row as MovieMeta).release_date
          : (row as SeriesMeta).first_air_date,
    };
    // Manual match only returns a provider id; it does not add a new library
    // title, so any match stays the same one-click operation.
    if (!onpick && (!hasKnownReleaseDate(target.releaseDate) || isFuture(target.releaseDate))) {
      confirmingRelease = target;
      return;
    }
    void choose(target);
  }

  async function confirmRelease() {
    const target = confirmingRelease;
    if (!target) return;
    confirmingRelease = null;
    await choose(target);
  }

  /**
   * Enter adds the site the arrows landed on. preventDefault is what stops the
   * browser activating the focused button as well, which would be two adds for
   * one keypress.
   */
  function onSiteRowKeydown(event: KeyboardEvent, hit: SiteMeta) {
    if (event.key === 'Enter') {
      event.preventDefault();
      void addSite(hit);
      return;
    }
    moveResultFocus(event, body);
  }

  async function addSite(hit: SiteMeta) {
    // Enter and a click can land on the same row in the same tick; the second
    // must not start a second catalogue walk.
    if (busyStashID !== null) return;
    busyStashID = hit.stash_id;
    try {
      const added = await api.addSite({
        stash_id: hit.stash_id,
        monitored: monitorOnAdd,
        search_now: searchOnAdd,
        library_id: targetLibraryID || undefined,
      });
      // The site page it navigates to will be empty for a moment: the add
      // answers as soon as the row exists and the scenes arrive from a
      // background job. Saying so is the difference between "still working"
      // and "this site has nothing".
      pushToast(`Added ${added.title}. Cataloguing scenes in the background.`, 'success');
      onclose();
      navigate(siteHref(added));
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busyStashID = null;
    }
  }

  async function choose(target: AddTarget) {
    const { kind: targetKind, row } = target;
    busyID = row.tmdb_id;
    addFault = null;
    try {
      if (onpick) {
        await onpick(targetKind, row.tmdb_id);
        return;
      }
      if (targetKind === 'movie') {
        const added = await api.addMovie({
          tmdb_id: row.tmdb_id,
          monitored: monitorOnAdd,
          search_now: searchOnAdd,
          library_id: targetLibraryID || undefined,
        });
        pushToast(`Added ${added.title}`, 'success');
        if (onadded) {
          onadded('movie', { id: added.id, title: added.title });
          return;
        }
        onclose();
        navigate(`/movies/${added.id}`);
      } else {
        const added = await api.addSeries({
          tmdb_id: row.tmdb_id,
          monitored: monitorOnAdd,
          search_missing: searchOnAdd,
          library_id: targetLibraryID || undefined,
        });
        pushToast(`Added ${added.title}`, 'success');
        if (onadded) {
          onadded('series', { id: added.id, title: added.title });
          return;
        }
        onclose();
        navigate(`/series/${added.id}`);
      }
    } catch (err) {
      // A missing or rejected key is not a toast: it names a fix, and the
      // dialog stays open on the empty state that carries it.
      const fault = metadataFault(err);
      if (fault) addFault = fault;
      else pushToast(errorText(err), 'danger');
    } finally {
      busyID = null;
    }
  }
</script>

<Modal {title} {onclose}>
  <div bind:this={body} class="flex flex-col gap-4 p-4">
    {#if !fixedKind}
      <div class="flex flex-wrap items-center gap-2">
        <div class="flex gap-2" role="tablist" aria-label="Search type">
          {#each scopes as tab (tab.key)}
            <button
              type="button"
              role="tab"
              aria-selected={scope === tab.key}
              onclick={() => (kind = tab.key)}
              class="h-7 rounded-full border px-3 text-sm transition-colors duration-150 ease-out
                     {scope === tab.key
                ? 'border-accent bg-accent-tint text-accent-text'
                : 'border-border bg-surface text-ink-secondary hover:bg-raised hover:text-ink'}">
              {tab.label}
            </button>
          {/each}
        </div>
        <span class="ml-0 flex items-center gap-1 text-xs text-ink-muted sm:ml-auto" aria-hidden="true">
          <kbd class="rounded-sm bg-surface px-1.5 py-0.5 font-mono">Tab</kbd>
          to switch
        </span>
      </div>
    {/if}

    <TextInput
      bind:value={search.query}
      type="search"
      autofocus
      onkeydown={onSearchKeydown}
      placeholder={scope === 'site'
        ? 'Site name…'
        : scope === 'movie'
          ? 'Search TMDB for a movie…'
          : 'Search TMDB for a series…'}
      ariaLabel={scope === 'site' ? 'Search the metadata provider for a site' : 'Search TMDB'} />

    {#if credentialFault}
      <!-- TMDB is what names a movie or a series, so with no usable key there
           is nothing for this dialog to search and nothing for it to add. The
           one thing that would change that is a link, not a retry. -->
      <CredentialEmptyState fault={credentialFault} />
    {:else if search.error}
      <!-- The retry belongs to the adult scope: a stash-box endpoint that is
           down or unconfigured fails every search the same way and recovers on
           its own, which is what a Retry is for. TMDB's failures here are
           unchanged from before this scope existed. -->
      <LoadError message={search.error} onretry={scope === 'site' ? search.retry : undefined} />
    {:else if search.loading}
      <SearchSkeleton />
    {:else if scope === 'site'}
      {#if search.idle}
        <EmptyState
          icon="search"
          title="Keep typing"
          message="Type at least two characters to look up a site." />
      {:else if sites.length === 0}
        <EmptyState
          icon="search"
          title="No matches"
          message={search.trimmed === ''
            ? 'The metadata provider offered nothing to start from. Search for a site by name.'
            : `No site matches “${search.trimmed}”. Try the network above it, or an alias off a release name.`} />
      {:else}
        <ul class="flex flex-col gap-2">
          {#each sites as hit (hit.stash_id)}
            <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
            <li
              class="flex items-center gap-3 rounded-md border border-border bg-surface p-3
                     transition-colors duration-150 ease-out hover:bg-raised focus-within:bg-raised"
              onkeydown={(event) => onSiteRowKeydown(event, hit)}>
              <div class="w-10 shrink-0">
                <Poster path={hit.image_url} alt="" fallbackIcon="flame" />
              </div>
              <div class="min-w-0 flex-1">
                <p class="truncate text-base font-medium text-ink" title={hit.name}>{hit.name}</p>
                <p class="flex flex-wrap items-center gap-2 text-sm text-ink-secondary">
                  {#if hit.parent_name}
                    <span>{hit.parent_name}</span>
                  {/if}
                  <!-- A release name carries whichever alias its packager saw,
                       so the aliases are what make the right row identifiable. -->
                  {#if hit.aliases.length > 0}
                    <span class="truncate" title={`also ${hit.aliases.join(', ')}`}>
                      also {hit.aliases.join(', ')}
                    </span>
                  {/if}
                </p>
              </div>
              {#if hit.in_library}
                <Badge tone="success">In library</Badge>
              {:else}
                <Button
                  variant="primary"
                  size="sm"
                  disabled={busyStashID !== null}
                  onclick={() => void addSite(hit)}>
                  {busyStashID === hit.stash_id ? 'Adding…' : 'Add'}
                </Button>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    {:else if search.idle}
      <EmptyState
        icon="search"
        title="Search the metadata provider"
        message="Type at least two characters to look up a title on TMDB." />
    {:else if rows.length === 0}
      <EmptyState
        icon="search"
        title="No matches"
        message="TMDB returned nothing for “{search.trimmed}”. Try the original-language title, or add the year." />
    {:else}
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
      <ul class="flex flex-col gap-2" onkeydown={onListKeydown}>
        {#each rows as row (row.tmdb_id)}
          {@const releaseDate = 'release_date' in row ? row.release_date : row.first_air_date}
          {@const rating = ratingPresentation(row.vote_average, row.vote_count, releaseDate)}
          <li class="flex items-start gap-3 rounded-md border border-border p-2 transition-colors duration-150 ease-out hover:bg-raised focus-within:bg-raised">
            <div class="w-12 shrink-0">
              <Poster
                path={row.poster_url}
                alt=""
                fallbackIcon={scope === 'movie' ? 'film' : 'tv'} />
            </div>
            <div class="min-w-0 flex-1">
              <p class="truncate text-base font-medium text-ink" title={row.title}>{row.title}</p>
              <div class="mt-1 flex flex-wrap items-center gap-1.5">
                <Badge mono tone="neutral">{row.year > 0 ? row.year : 'Year unknown'}</Badge>
                <Badge mono tone="neutral" title={rating.title}>
                  {rating.text ?? rating.title}
                </Badge>
              </div>
              <p
                class="line-clamp-2 text-sm text-ink-secondary"
                title={row.overview || 'No overview available.'}>
                {row.overview || 'No overview available.'}
              </p>
            </div>
            <Button
              variant="primary"
              size="sm"
              disabled={busyID !== null}
              onclick={() => select(row)}>
              {busyID === row.tmdb_id ? 'Working…' : onpick ? 'Match' : 'Add'}
            </Button>
          </li>
        {/each}
      </ul>
    {/if}

    {#if !onpick}
      <!-- Add-mode only: the manual-match picker re-points an existing file at
           an item that is already in the library, so neither option applies to
           it. Every add scope gets the same pair, sites included — a site is
           followed and searched exactly like a series.

           The search box only exists while monitoring is on, because a search
           reads the wanted list and nothing unmonitored is on it. -->
      <div class="flex flex-col gap-2">
        {#if libraryChoices.length > 1}
          <!-- Only rendered when the choice exists: with a single library of
               the kind there is nothing to decide and the select would be a
               question with one answer. -->
          <label
            class="flex items-center gap-3 rounded-md border border-border bg-raised px-3 py-2">
            <span class="text-base text-ink">Library</span>
            <select
              bind:value={targetLibraryID}
              class="h-8 flex-1 rounded-sm border border-border-strong bg-raised px-2 text-md text-ink focus:border-accent focus:outline-none"
              aria-label="Target library">
              {#each libraryChoices as choice (choice.id)}
                <option value={choice.is_default ? 0 : choice.id}>
                  {choice.name}{choice.is_default ? ' (default)' : ''}
                </option>
              {/each}
            </select>
          </label>
        {/if}
        <label class="flex items-center gap-3 rounded-md border border-border bg-raised px-3 py-2">
          <input
            type="checkbox"
            checked={monitorOnAdd}
            onchange={(event) => setMonitorOnAdd(event.currentTarget.checked)}
            class="size-4 accent-accent" />
          <span class="text-base text-ink">Add and monitor</span>
        </label>
        {#if monitorOnAdd}
          <label
            class="ml-6 flex items-center gap-3 rounded-md border border-border bg-raised px-3 py-2">
            <input
              type="checkbox"
              checked={searchOnAdd}
              onchange={(event) => (searchOnAdd = event.currentTarget.checked)}
              class="size-4 accent-accent" />
            <span class="text-base text-ink">Start searching immediately</span>
          </label>
        {/if}
      </div>
    {/if}
  </div>
</Modal>

{#if confirmingRelease}
  {@const target = confirmingRelease}
  {@const unknownReleaseDate = !hasKnownReleaseDate(target.releaseDate)}
  <Modal
    title={unknownReleaseDate ? 'Add title with unknown release date' : 'Add unreleased title'}
    width="max-w-lg"
    onclose={() => (confirmingRelease = null)}>
    <div class="flex flex-col gap-3 p-4">
      <p class="text-base text-ink">
        {#if unknownReleaseDate}
          <span class="font-medium">{target.row.title}</span>'s release date is unknown.
        {:else}
          <span class="font-medium">{target.row.title}</span> has not been released yet.
        {/if}
      </p>
      <p class="text-base text-ink-secondary">
        Add it to the library anyway?
      </p>
    </div>

    {#snippet footer()}
      <Button
        variant="ghost"
        disabled={busyID !== null}
        onclick={() => (confirmingRelease = null)}>Cancel</Button>
      <Button variant="primary" disabled={busyID !== null} onclick={confirmRelease}>
        {unknownReleaseDate ? 'Add title anyway' : 'Add unreleased title'}
      </Button>
    {/snippet}
  </Modal>
{/if}