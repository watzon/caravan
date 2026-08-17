<script lang="ts">
  /**
   * The ⌘K add flow (SPEC §9 steps 1-2): search, then "add to library" with
   * monitoring on. Also reusable as a plain picker for the scan-review manual
   * match, where `onpick` replaces the add call.
   *
   * One row of tabs, one target. In add mode a tab IS a library: the shelf the
   * item lands on, and therefore the provider chain that answers the search.
   * The tab's kind decides what is searched — films, television, both at once
   * on an anime shelf, or the stash-box catalogue on an adult one. Adult tabs
   * exist only where the module does — `session.adult`, the same single boolean
   * the sidebar row reads — and a caller cannot select one into being: see
   * `fixedScope`. With it absent, nothing here touches an adult type or an
   * adult route.
   *
   * Pick mode (`onpick`) keeps the bare Movies/Series pills it has always had:
   * a manual match names a title, it does not file one, so the library list is
   * neither loaded nor its business. The same fallback carries a caller who
   * cannot read the admin-only library list at all.
   */
  import { untrack } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Library, LibraryKind, MovieMeta, SearchResults, SeriesMeta, SiteMeta } from '../api/types';
  import { siteHref } from '../adult';
  import { ratingPresentation } from '../discover';
  import { isFuture } from '../format';
  import { metadataFault, type CredentialFault } from '../credentials';
  import { LIBRARY_KIND_ORDER, libraryKindAccepts, sessionLibraryBySlug } from '../library';
  import { navigate, router } from '../router.svelte';
  import { readLibraryFilter } from '../shelf';
  import { libraries } from '../state/libraries.svelte';
  import { providers } from '../state/providers.svelte';
  import { session } from '../state/session.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { focusFirstResult, moveResultFocus } from '../typeahead';
  import { useI18n, type TranslationKey } from '../i18n.svelte';
  import { createTypeahead } from '../typeahead.svelte';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import CredentialEmptyState from './CredentialEmptyState.svelte';
  import EmptyState from './EmptyState.svelte';
  import Icon, { libraryIcon, type IconName } from './Icon.svelte';
  import LoadError from './LoadError.svelte';
  import Modal from './Modal.svelte';
  import Poster from './Poster.svelte';
  import SearchSkeleton from './SearchSkeleton.svelte';
  import TextInput from './TextInput.svelte';

  /**
   * What the dialog is searching. `anime` is the one scope that is two: an
   * anime shelf holds films AND series, so its tab asks both halves of the
   * chain and shows one list — the same "two tables, one grid" the /anime
   * screen renders.
   */
  type Scope = 'movie' | 'series' | 'anime' | 'site';

  /**
   * The vocabulary each scope files its results under, and the scope each kind
   * of shelf opens on. Two total maps rather than a ternary per question: every
   * kind branch in this dialog goes through one of them, so a kind this build
   * gains is a line here rather than a search for the ternaries that missed it.
   */
  const SCOPE_KIND: Record<Scope, LibraryKind> = {
    movie: 'movie',
    series: 'tv',
    anime: 'anime',
    site: 'adult',
  };
  const KIND_SCOPE: Record<LibraryKind, Scope> = {
    movie: 'movie',
    tv: 'series',
    anime: 'anime',
    adult: 'site',
  };
  /** The label a bare-kind tab wears; a library tab wears the library's name. */
  const SCOPE_LABELS: Record<Scope, TranslationKey> = {
    movie: 'component.media.movies',
    series: 'component.media.series',
    anime: 'component.media.anime',
    site: 'component.media.adult',
  };

  /**
   * The scopes a pick-mode caller can be handed back.
   *
   * Sites are not among them, and that is a statement about the flow rather
   * than a gap: pick mode exists for the scan-review manual match, which points
   * a file on disk at a movie or a series. A site is identified by a stash-box
   * id and imported by a different endpoint entirely, so pick mode simply has
   * no adult scope.
   */
  type PickKind = 'movie' | 'series';
  interface AddTarget {
    kind: PickKind;
    row: MovieMeta | SeriesMeta;
    releaseDate: string;
  }

  interface Props {
    onclose: () => void;
    /**
     * Restrict the picker to one scope (scan review knows what it parsed).
     *
     * In add mode it narrows the tabs to the shelves that accept that scope
     * rather than removing them: a fixed `movie` still has to land somewhere,
     * and an anime shelf accepts it — but its tab then searches films alone,
     * because the caller already knows which half it needs.
     */
    kind?: Scope | null;
    /**
     * Which tab starts selected. It names a kind rather than a library, so the
     * shelf screen that opened the dialog gets that kind's DEFAULT shelf — the
     * one an untargeted add would have landed on. A `?library=` on the URL
     * behind the dialog is more specific and wins; see `openingTarget`.
     */
    initialKind?: Scope;
    /** Prefill for the manual-match flow. */
    initialQuery?: string;
    title?: string;
    /**
     * The library this dialog is about, when the caller already knows.
     *
     * Pick mode reads it as the chain that answers the search: scan review
     * knows which library the parked file belongs to, and the same chain that
     * would have identified the file automatically is the one the user
     * searches by hand.
     *
     * Add mode reads it as THE target, and narrows the tab row to that one
     * shelf. The grab tie is why: "add this title, then tie this release to
     * it" is a sentence about one library, and the server refuses a tie to an
     * item another library owns — so a tab offering a second shelf would be
     * offering a path that ends in a refusal.
     */
    libraryID?: number;
    /**
     * When supplied the modal picks instead of adding: the caller decides what
     * to do with the chosen row. It is handed the whole hit rather than an id
     * because a chain hit is identified by `provider` + `provider_ref`, and a
     * hit from anywhere but TMDB carries a zero `tmdb_id`. Its presence also
     * removes the adult scope — see PickKind.
     */
    onpick?: (kind: PickKind, row: MovieMeta | SeriesMeta) => Promise<void> | void;
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
    title: suppliedTitle,
    libraryID = 0,
    onpick,
    onadded,
  }: Props = $props();
  const { t } = useI18n();
  let title = $derived(suppliedTitle ?? t('component.addItem.title'));

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

  /**
   * The row whose add is in flight, named by `rowKey` rather than by tmdb_id:
   * every hit from a provider that is not TMDB carries tmdb_id 0, so on a
   * merged chain a numeric busy id would mark every one of them at once.
   */
  let busyKey = $state<string | null>(null);
  /** The site whose add is in flight; sites are named by a string, not an id. */
  let busyStashID = $state<string | null>(null);
  /** A future- or unknown-release title waiting for the user's explicit approval. */
  let confirmingRelease = $state<AddTarget | null>(null);

  /**
   * Whether the adult scope is on screen. `session.adult` is the server's own
   * combined answer (module on AND this account reaches it), the same one the
   * sidebar row reads, and an unknown identity reads as false.
   *
   * Pick mode drops it too: a manual match names a movie or a series, and a
   * hand-back caller (`onadded`) builds a tie, which names one of the same
   * two. Neither could do anything with a site, so neither is offered one.
   */
  let siteScope = $derived(!onpick && !onadded && session.adult);

  /**
   * What identifies one result row, for the keyed each and the busy marker.
   *
   * `provider:provider_ref` is the only identity a merged chain has: two
   * providers number different things the same way, and every non-TMDB hit
   * carries tmdb_id 0 — so keying on tmdb_id gave a whole AniList page the
   * same key and crashed the list. tmdb_id is the fallback for a stub from
   * before the pair existed, where provider_ref is empty.
   */
  function rowKey(entry: ResultRow): string {
    const { row } = entry;
    // The type leads because the anime tab shows both catalogues at once, and
    // one provider numbers its films and its series separately: TMDB's movie
    // 754 and series 754 are different titles wearing the same pair.
    return `${entry.type}:${row.provider ?? ''}:${row.provider_ref || row.tmdb_id}`;
  }

  /** The ref pair an add sends, or nothing when the row predates it. */
  function refOf(row: MovieMeta | SeriesMeta): { provider: string; provider_ref: string } | null {
    // Both or neither: the server rejects half a pair, because a ref read in
    // the wrong vocabulary is a different title rather than a failed lookup.
    if (!row.provider || !row.provider_ref) return null;
    return { provider: row.provider, provider_ref: row.provider_ref };
  }

  // Add mode targets a library, so it needs the list. Pick mode adds nothing,
  // so it neither needs the list nor may fetch it — and the fetch is lazy for
  // the store's reason: /libraries is admin-only.
  $effect(() => {
    if (!onpick) void libraries.load();
  });

  /**
   * One tab: what the search asks, and where the add lands.
   *
   * A library tab and a bare-kind tab are the same thing to everything below
   * this point, which is why there is one list and one selection rather than
   * two templates. The difference is only in how the list is built.
   */
  interface Target {
    /** Tab identity. Stable across list reloads, unlike an index. */
    key: string;
    label: string;
    /** A library tab wears its shelf's glyph; a bare-kind tab wears none. */
    icon: IconName | null;
    scope: Scope;
    /** 0 is "name no library" — the server's own spelling for "the default". */
    libraryID: number;
    /** The row behind a library tab, for its provider chain. */
    library: Library | null;
  }

  /**
   * The fixed scope actually in force. A caller that asks for `site` on a
   * browser the module is invisible to gets Movies instead — the gate is here
   * rather than at the call sites so there is exactly one of it, and so no path
   * can reach an adult request by passing a prop.
   */
  let fixedScope = $derived<Scope | null>(
    fixedKind === 'site' && !siteScope ? 'movie' : fixedKind,
  );

  /**
   * The bare-kind tabs: the pill row this dialog showed before a tab was a
   * library. They carry the caller's `libraryID` because that is the only
   * library a caller without tabs has named.
   */
  function kindTarget(scope: Scope): Target {
    return {
      key: `kind:${scope}`,
      label: t(SCOPE_LABELS[scope]),
      icon: null,
      scope,
      libraryID,
      library: null,
    };
  }

  let kindTargets = $derived<Target[]>(
    fixedScope
      ? [kindTarget(fixedScope)]
      : [kindTarget('movie'), kindTarget('series'), ...(siteScope ? [kindTarget('site')] : [])],
  );

  /**
   * Sidebar order: kind in LIBRARY_KIND_ORDER, then the kind's default, then
   * id. Adult is absent from that order — the sidebar collapses it into one
   * row — so it sorts last here rather than first, which is where an unfound
   * index would otherwise put it.
   */
  function kindRank(kind: LibraryKind): number {
    const at = LIBRARY_KIND_ORDER.indexOf(kind);
    return at === -1 ? LIBRARY_KIND_ORDER.length : at;
  }

  function libraryTarget(lib: Library): Target {
    return {
      key: `lib:${lib.id}`,
      label: lib.name,
      icon: libraryIcon(lib.kind, lib.icon),
      // A fixed kind wins over the shelf's own vocabulary: an anime shelf
      // speaks both, and a caller that already knows which half it needs — the
      // grab tie, the manual match — must not be offered the other one.
      scope: fixedScope ?? KIND_SCOPE[lib.kind],
      libraryID: lib.id,
      library: lib,
    };
  }

  /**
   * The shelves this admin may add to, one tab each.
   *
   * `targetable()` is what drops the dormant shelves — a shelf every content
   * route 404s on is not somewhere to file anything — and three filters follow
   * it, each a rule stated elsewhere:
   *   - the caller's shelf alone, where it named one. See `libraryID`.
   *   - adult shelves only where the module is visible, exactly as the Adult
   *     pill was gated. `siteScope` is the one boolean that decides it.
   *   - when the caller fixed a kind, only the shelves that accept it —
   *     `libraryKindAccepts` mirrors the server's own rule
   *     (core.LibraryKindAccepts), so no tab can offer a target the add would
   *     be refused for.
   *
   * Empty in pick mode by construction rather than by template: the store is a
   * session-wide singleton another admin surface may have filled, and a manual
   * match is still not a thing that files anything.
   */
  let libraryTargets = $derived<Target[]>(
    onpick
      ? []
      : libraries
          .targetable()
          .filter((lib) => libraryID === 0 || lib.id === libraryID)
          .filter((lib) => lib.kind !== 'adult' || siteScope)
          .filter(
            (lib) => fixedScope === null || libraryKindAccepts(lib.kind, SCOPE_KIND[fixedScope]),
          )
          .sort(
            (a, b) =>
              kindRank(a.kind) - kindRank(b.kind) ||
              Number(b.is_default) - Number(a.is_default) ||
              a.id - b.id,
          )
          .map(libraryTarget),
  );

  /**
   * The tabs. Libraries when there are any, and the bare kinds when there are
   * none — a member on the Adult screen never loads the admin-only list, and an
   * admin whose list failed to load still has a dialog that searches.
   */
  let targets = $derived<Target[]>(libraryTargets.length > 0 ? libraryTargets : kindTargets);

  /**
   * The library the screen behind the dialog is filtered to, if any. Standing
   * on one shelf and pressing Add says where the item should go at least as
   * clearly as picking a tab does, so it opens on that shelf's tab.
   */
  let urlLibraryID = $derived.by(() => {
    const fromQuery = readLibraryFilter(router.params);
    if (fromQuery > 0) return fromQuery;
    if (router.match?.pattern === '/l/:slug') {
      return sessionLibraryBySlug(session.user, router.match.params.slug ?? '')?.id ?? 0;
    }
    return 0;
  });
  /** The kind the caller asked to open on; `site` is gated exactly as above. */
  let seedScope = $derived<Scope>(
    fixedScope ?? (initialKind === 'site' && !siteScope ? 'movie' : initialKind),
  );

  /**
   * The tab the dialog opens on, most specific answer first: the URL's shelf,
   * then that kind's DEFAULT shelf (the tabs are sorted default-first, so the
   * first of a kind IS the default), then whatever leads the row.
   */
  function openingTarget(list: Target[]): Target | undefined {
    return (
      (urlLibraryID > 0 ? list.find((tab) => tab.libraryID === urlLibraryID) : undefined) ??
      list.find((tab) => tab.library?.kind === SCOPE_KIND[seedScope]) ??
      list.find((tab) => tab.scope === seedScope) ??
      list[0]
    );
  }

  let selectedKey = $state('');
  // The list arrives asynchronously — /libraries is a request — so the opening
  // choice is made whenever the selected tab is not among the tabs rather than
  // once at mount. It is also what re-points the dialog when a shelf goes
  // dormant underneath it.
  $effect(() => {
    const list = targets;
    if (untrack(() => list.some((tab) => tab.key === selectedKey))) return;
    selectedKey = openingTarget(list)?.key ?? '';
  });

  /** The selected tab. `targets` is never empty, so neither is this. */
  let target = $derived<Target>(
    targets.find((tab) => tab.key === selectedKey) ?? targets[0] ?? kindTarget('movie'),
  );
  let scope = $derived<Scope>(target.scope);
  /** Where the add lands and which chain answers: the tab, and only the tab. */
  let targetLibraryID = $derived(target.libraryID);
  let targetLibrary = $derived(target.library);

  // Debounce, minimum length and latest-wins cancellation live in the shared
  // typeahead; only the request and the shape of an empty answer differ by
  // scope.
  const search = createTypeahead<SearchResults & { sites: SiteMeta[] }>({
    initial: untrack(() => initialQuery),
    blank: () => ({ movies: [], series: [], sites: [], providers: [], library_id: 0 }),
    run: async (q, signal) => {
      // Read the tab once: the request belongs to the tab that started it, and
      // a tab pressed mid-flight aborts this one rather than steering it.
      const { scope: asked, libraryID: shelf } = target;
      if (asked === 'site') {
        return {
          movies: [],
          series: [],
          sites: await api.searchSites(q, signal),
          providers: [],
          library_id: 0,
        };
      }
      // `all` is the endpoint's own word for "both halves", and named a shelf
      // it means both halves of THAT shelf — which for an anime library is the
      // films and the series it holds together (internal/api.handleSearch, via
      // core.LibraryKindAccepts). So the tab that shows two catalogues asks one
      // question, and the merging of the two chain walks' providers and errors
      // is the server's rather than a second copy of it here.
      const found = await api.search(q, asked === 'anime' ? 'all' : asked, shelf || undefined, signal);
      return {
        movies: found.movies ?? [],
        series: found.series ?? [],
        sites: [],
        providers: found.providers ?? [],
        library_id: found.library_id ?? 0,
      };
    },
    // The tab is the whole question: it names both what is searched and which
    // library's chain answers, so pressing one re-runs the search.
    depends: () => target.key,
  });

  /**
   * One result row: the hit, and which of the two catalogues it came from.
   *
   * `type` travels with the row rather than being re-derived from the scope,
   * because the anime tab's list is BOTH — the same shape /anime's merged grid
   * carries, for the same reason. Films lead the series rather than interleaving
   * with them: neither provider ranks one against the other, so a stable
   * grouping the badge explains beats an order nothing justifies.
   */
  interface ResultRow {
    type: PickKind;
    row: MovieMeta | SeriesMeta;
  }

  /**
   * Which catalogues a tab puts on screen. The server already answers with the
   * unasked half empty, but the tab decides what it shows rather than trusting
   * that: a shelf tab showing a kind it cannot hold would offer an add the
   * server would refuse.
   */
  const SCOPE_TYPES: Record<Scope, PickKind[]> = {
    movie: ['movie'],
    series: ['series'],
    anime: ['movie', 'series'],
    site: [],
  };

  let rows = $derived.by<ResultRow[]>(() => {
    const halves = { movie: search.results.movies, series: search.results.series };
    return SCOPE_TYPES[scope].flatMap((type) =>
      halves[type].map((row): ResultRow => ({ type, row })),
    );
  });
  let sites = $derived<SiteMeta[]>(search.results.sites);
  /**
   * Whether a row has to say which catalogue it came from. Only a tab showing
   * both at once does; anywhere else the tab has already said it.
   */
  let showTypes = $derived(SCOPE_TYPES[scope].length > 1);

  /**
   * The chain in force, for the copy and the badges.
   *
   * The last answer is authoritative — it is the chain that actually ran — but
   * it is empty before the first search, so the target library's own chain
   * stands in until then. Both are empty on a browser that never loaded the
   * admin-only library list, and that is the case the TMDB wording covers.
   */
  let chain = $derived<string[]>(
    search.results.providers.length > 0
      ? search.results.providers
      : (targetLibrary?.providers ?? []),
  );
  /**
   * Whether more than one provider is answering. It gates both the badges and
   * the copy: on the overwhelmingly common single-provider install a badge on
   * every row is noise, and "the metadata providers" is a plural describing
   * one thing.
   */
  let manyProviders = $derived(chain.length > 1);
  /**
   * Whether the copy may name TMDB — the one claim in this dialog that can be
   * false rather than merely clumsy. An anime shelf chained to AniList alone
   * searches AniList, so "Search TMDB for a film" would be telling the user
   * where their results did not come from. An unknown chain still reads as
   * TMDB: that is the stock install, and it is what a browser that never loaded
   * the admin-only library list has to go on.
   */
  let namesTmdb = $derived(chain.length === 0 || (chain.length === 1 && chain[0] === 'tmdb'));

  /**
   * The search box's copy, per scope and per whether TMDB is what answers. A
   * total map rather than nested ternaries: adding the anime tab added a row
   * here instead of a branch in the template.
   */
  const PLACEHOLDERS: Record<Scope, { tmdb: TranslationKey; providers: TranslationKey }> = {
    movie: {
      tmdb: 'component.addItem.movieTmdbPlaceholder',
      providers: 'component.addItem.movieProviderPlaceholder',
    },
    series: {
      tmdb: 'component.addItem.seriesTmdbPlaceholder',
      providers: 'component.addItem.seriesProviderPlaceholder',
    },
    anime: {
      tmdb: 'component.addItem.animeTmdbPlaceholder',
      providers: 'component.addItem.animeProviderPlaceholder',
    },
    // The stash-box catalogue is not TMDB's under any chain, so both answers
    // are the one that never names a metadata provider.
    site: {
      tmdb: 'component.addItem.sitePlaceholder',
      providers: 'component.addItem.sitePlaceholder',
    },
  };
  let placeholder = $derived(t(PLACEHOLDERS[scope][namesTmdb ? 'tmdb' : 'providers']));
  let searchLabel = $derived(
    scope === 'site'
      ? t('component.addItem.siteSearchLabel')
      : namesTmdb
        ? t('component.addItem.tmdbSearchLabel')
        : t('component.addItem.providerSearchLabel'),
  );
  /**
   * What the tab row is choosing between. In add mode it is a shelf; where the
   * tabs are bare kinds it is still what it always was, a search type.
   */
  let tablistLabel = $derived(
    libraryTargets.length > 0
      ? t('component.addItem.targetLibrary')
      : t('component.addItem.searchType'),
  );

  // Named badges need the provider list, which is admin-only — so it is
  // fetched only once a chain has actually come back longer than one, and a
  // caller that cannot fetch it falls back to the raw provider id.
  $effect(() => {
    if (search.results.providers.length > 1) void providers.load();
  });

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

  // Palette-style keys while typing: Tab cycles the tabs (so it is not focus
  // navigation here), ArrowDown jumps to the first result's button, which is
  // where Tab would otherwise have gone. However many tabs there are, the cycle
  // is the same one — and with a single tab there is nothing to cycle, so Tab
  // goes back to being the browser's.
  function onSearchKeydown(event: KeyboardEvent) {
    if (event.key === 'Tab' && !event.shiftKey && targets.length > 1) {
      event.preventDefault();
      const at = targets.findIndex((tab) => tab.key === selectedKey);
      // -1 (nothing selected yet) wraps to the first tab, not past it.
      selectedKey = targets[(at + 1) % targets.length]?.key ?? '';
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

  function select(entry: ResultRow) {
    // The row says which catalogue it came from, so the anime tab's mixed list
    // routes each add to the endpoint that half belongs to.
    const { type: targetKind, row } = entry;
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
      pushToast(t('component.addItem.addedSiteCataloguing', { title: added.title }), 'success');
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
    busyKey = rowKey({ type: targetKind, row });
    addFault = null;
    // tmdb_id travels beside the pair rather than instead of it: the server
    // lets the pair win, and a stub with no pair still identifies itself.
    const ref = refOf(row);
    try {
      if (onpick) {
        await onpick(targetKind, row);
        return;
      }
      if (targetKind === 'movie') {
        const added = await api.addMovie({
          tmdb_id: row.tmdb_id,
          ...(ref ?? {}),
          monitored: monitorOnAdd,
          search_now: searchOnAdd,
          library_id: targetLibraryID || undefined,
        });
        pushToast(t('component.addItem.added', { title: added.title }), 'success');
        if (onadded) {
          onadded('movie', { id: added.id, title: added.title });
          return;
        }
        onclose();
        navigate(`/movies/${added.id}`);
      } else {
        const added = await api.addSeries({
          tmdb_id: row.tmdb_id,
          ...(ref ?? {}),
          monitored: monitorOnAdd,
          search_missing: searchOnAdd,
          library_id: targetLibraryID || undefined,
        });
        pushToast(t('component.addItem.added', { title: added.title }), 'success');
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
      busyKey = null;
    }
  }
</script>

<Modal {title} {onclose}>
  <div bind:this={body} class="flex flex-col gap-4 p-4">
    <!-- One row for both worlds: a tab per shelf in add mode, a tab per kind
         where there are no shelves to offer. A single tab is not a choice, so
         it draws nothing — which is what a fixed-kind caller with one legal
         target sees. -->
    {#if targets.length > 1}
      <div class="flex flex-wrap items-center gap-2">
        <div class="flex flex-wrap gap-2" role="tablist" aria-label={tablistLabel}>
          {#each targets as tab (tab.key)}
            <button
              type="button"
              role="tab"
              aria-selected={target.key === tab.key}
              onclick={() => (selectedKey = tab.key)}
              class="flex h-7 items-center gap-1.5 rounded-full border px-3 text-sm transition-colors duration-150 ease-out
                     {target.key === tab.key
                ? 'border-accent bg-accent-tint text-accent-text'
                : 'border-border bg-surface text-ink-secondary hover:bg-raised hover:text-ink'}">
              {#if tab.icon}
                <Icon name={tab.icon} size={14} />
              {/if}
              {tab.label}
            </button>
          {/each}
        </div>
        <span class="ml-0 flex items-center gap-1 text-xs text-ink-muted sm:ml-auto" aria-hidden="true">
          <kbd class="rounded-sm bg-surface px-1.5 py-0.5 font-mono">Tab</kbd>
          {t('component.addItem.switchHint')}
        </span>
      </div>
    {/if}

    <TextInput
      bind:value={search.query}
      type="search"
      autofocus
      onkeydown={onSearchKeydown}
      {placeholder}
      ariaLabel={searchLabel} />

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
          title={t('component.addItem.keepTyping')}
          message={t('component.addItem.siteMinimumCharacters')} />
      {:else if sites.length === 0}
        <EmptyState
          icon="search"
          title={t('component.addItem.noMatches')}
          message={search.trimmed === ''
            ? t('component.addItem.noSites')
            : t('component.addItem.noSiteMatches', { query: search.trimmed })} />
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
                    <span class="truncate" title={t('component.addItem.aliases', { aliases: hit.aliases.join(', ') })}>
                      {t('component.addItem.aliases', { aliases: hit.aliases.join(', ') })}
                    </span>
                  {/if}
                </p>
              </div>
              {#if hit.in_library}
                <Badge tone="success">{t('component.status.inLibrary')}</Badge>
              {:else}
                <Button
                  variant="primary"
                  size="sm"
                  disabled={busyStashID !== null}
                  onclick={() => void addSite(hit)}>
                  {busyStashID === hit.stash_id ? t('component.actions.adding') : t('component.actions.add')}
                </Button>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    {:else if search.idle}
      <EmptyState
        icon="search"
        title={manyProviders
          ? t('component.addItem.providerSearchTitle')
          : t('component.addItem.providerSearchTitleSingle')}
        message={namesTmdb
          ? t('component.addItem.tmdbTitleMinimumCharacters')
          : t('component.addItem.titleMinimumCharacters')} />
    {:else if rows.length === 0}
      <EmptyState
        icon="search"
        title={t('component.addItem.noMatches')}
        message={namesTmdb
          ? t('component.addItem.noTmdbMatches', { query: search.trimmed })
          : t('component.addItem.noProviderMatches', { query: search.trimmed })} />
    {:else}
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
      <ul class="flex flex-col gap-2" onkeydown={onListKeydown}>
        {#each rows as entry (rowKey(entry))}
          {@const row = entry.row}
          {@const releaseDate = 'release_date' in row ? row.release_date : row.first_air_date}
          {@const rating = ratingPresentation(row.vote_average, row.vote_count, releaseDate)}
          <li class="flex items-start gap-3 rounded-md border border-border p-2 transition-colors duration-150 ease-out hover:bg-raised focus-within:bg-raised">
            <div class="w-12 shrink-0">
              <Poster
                path={row.poster_url}
                alt=""
                fallbackIcon={entry.type === 'movie' ? 'film' : 'tv'} />
            </div>
            <div class="min-w-0 flex-1">
              <p class="truncate text-base font-medium text-ink" title={row.title}>{row.title}</p>
              <div class="mt-1 flex flex-wrap items-center gap-1.5">
                <!-- Only where both catalogues are on screen at once: on every
                     other tab the tab itself has already said which one this
                     is, and a badge repeating it is noise. -->
                {#if showTypes}
                  <Badge tone="neutral">
                    {entry.type === 'movie'
                      ? t('component.addItem.typeMovie')
                      : t('component.addItem.typeSeries')}
                  </Badge>
                {/if}
                <Badge mono tone="neutral">{row.year > 0 ? row.year : t('component.addItem.yearUnknown')}</Badge>
                <Badge mono tone="neutral" title={rating.title}>
                  {rating.text ?? rating.title}
                </Badge>
                <!-- Only on a merged chain: with one provider answering, a
                     badge saying so on every row names the obvious. With
                     several it is what tells two same-titled hits apart. -->
                {#if manyProviders && row.provider}
                  <Badge tone="info">{providers.name(row.provider)}</Badge>
                {/if}
              </div>
              <p
                class="line-clamp-2 text-sm text-ink-secondary"
                title={row.overview || t('component.addItem.noOverview')}>
                {row.overview || t('component.addItem.noOverview')}
              </p>
            </div>
            <Button
              variant="primary"
              size="sm"
              disabled={busyKey !== null}
              onclick={() => select(entry)}>
              {busyKey === rowKey(entry) ? t('component.actions.working') : onpick ? t('component.actions.match') : t('component.actions.add')}
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
      <!-- No target select here any more: the tabs at the top ARE the target,
           and a second control answering the same question could disagree with
           them. -->
      <div class="flex flex-col gap-2">
        <label class="flex items-center gap-3 rounded-md border border-border bg-raised px-3 py-2">
          <input
            type="checkbox"
            checked={monitorOnAdd}
            onchange={(event) => setMonitorOnAdd(event.currentTarget.checked)}
            class="size-4 accent-accent" />
          <span class="text-base text-ink">{t('component.addItem.addAndMonitor')}</span>
        </label>
        {#if monitorOnAdd}
          <label
            class="ml-6 flex items-center gap-3 rounded-md border border-border bg-raised px-3 py-2">
            <input
              type="checkbox"
              checked={searchOnAdd}
              onchange={(event) => (searchOnAdd = event.currentTarget.checked)}
              class="size-4 accent-accent" />
            <span class="text-base text-ink">{t('component.addItem.searchNow')}</span>
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
    title={unknownReleaseDate ? t('component.addItem.unknownReleaseTitle') : t('component.addItem.unreleasedTitle')}
    width="max-w-lg"
    onclose={() => (confirmingRelease = null)}>
    <div class="flex flex-col gap-3 p-4">
      <p class="text-base text-ink">
        {unknownReleaseDate
          ? t('component.addItem.unknownReleaseDate', { title: target.row.title })
          : t('component.addItem.unreleased', { title: target.row.title })}
      </p>
      <p class="text-base text-ink-secondary">
        {t('component.addItem.addAnyway')}
      </p>
    </div>

    {#snippet footer()}
      <Button
        variant="ghost"
        disabled={busyKey !== null}
        onclick={() => (confirmingRelease = null)}>{t('component.actions.cancel')}</Button>
      <Button variant="primary" disabled={busyKey !== null} onclick={confirmRelease}>
        {unknownReleaseDate ? t('component.addItem.addTitleAnyway') : t('component.addItem.unreleasedTitle')}
      </Button>
    {/snippet}
  </Modal>
{/if}