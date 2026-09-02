<script lang="ts">
  /**
   * App shell (DESIGN.md §5): 240px sidebar + full-bleed content on --color-bg,
   * with the top bar inside the content column.
   *
   * The first-run screen (SPEC §10.1) deliberately renders without the shell:
   * there is no library to navigate to until a storage root exists.
   */
  import { onMount } from 'svelte';
  import AddItemModal from './lib/components/AddItemModal.svelte';
  import Banner from './lib/components/Banner.svelte';
  import DirtyRecovery from './lib/components/DirtyRecovery.svelte';
  import Toasts from './lib/components/Toasts.svelte';
  import Sidebar from './lib/layout/Sidebar.svelte';
  import TopBar from './lib/layout/TopBar.svelte';
  import {
    isAdultRoute,
    memberAllowedRoute,
    numericParam,
    ordinalParam,
    type RoutePattern,
  } from './lib/router';
  import { sessionLibraryBySlug } from './lib/library';
  import { navigate, router, startRouter } from './lib/router.svelte';
  import Adult from './lib/routes/Adult.svelte';
  import AdultScene from './lib/routes/AdultScene.svelte';
  import AdultSite from './lib/routes/AdultSite.svelte';
  import Anime from './lib/routes/Anime.svelte';
  import Calendar from './lib/routes/Calendar.svelte';
  import Convert from './lib/routes/Convert.svelte';
  import Discover from './lib/routes/Discover.svelte';
  import DiscoverBrowse from './lib/routes/DiscoverBrowse.svelte';
  import DiscoverTitle from './lib/routes/DiscoverTitle.svelte';
  import ExploreAdult from './lib/routes/ExploreAdult.svelte';
  import ExploreTitles from './lib/routes/ExploreTitles.svelte';
  import Requests from './lib/routes/Requests.svelte';
  import History from './lib/routes/History.svelte';
  import FirstRun from './lib/routes/FirstRun.svelte';
  import Login from './lib/routes/Login.svelte';
  import MovieDetail from './lib/routes/MovieDetail.svelte';
  import Movies from './lib/routes/Movies.svelte';
  import NotFound from './lib/routes/NotFound.svelte';
  import Queue from './lib/routes/Queue.svelte';
  import ReleaseSearch from './lib/routes/ReleaseSearch.svelte';
  import SafeToEject from './lib/routes/SafeToEject.svelte';
import Search from './lib/routes/Search.svelte';
  import ScanReview from './lib/routes/ScanReview.svelte';
  import Series from './lib/routes/Series.svelte';
  import SeriesDetail from './lib/routes/SeriesDetail.svelte';
  import Wanted from './lib/routes/Wanted.svelte';
  import SettingsScreen from './lib/routes/Settings.svelte';
  import Button from './lib/components/Button.svelte';
  import { ADULT_EXPLORE_HREF, stashUnreachableBanner } from './lib/adult';
  import { unreachableClientBanner } from './lib/download';
  import { auth } from './lib/state/auth.svelte';
  import { startLiveUpdates } from './lib/state/live';
  import { session } from './lib/state/session.svelte';
  import { shutdown } from './lib/state/shutdown.svelte';
  import { system } from './lib/state/system.svelte';
  import { useI18n, type TranslationKey } from './lib/i18n.svelte';

  const { t } = useI18n();

  const TITLES: Record<RoutePattern, TranslationKey> = {
    '/first-run': 'app.title.firstRun',
    '/': 'app.title.discover',
    '/discover': 'app.title.discover',
    '/discover/movies': 'app.title.discover',
    '/discover/series': 'app.title.discover',
    '/discover/adult': 'app.title.discover',
    '/discover/network/:id': 'app.title.discover',
    '/discover/studio/:id': 'app.title.discover',
    '/discover/movie/:tmdbId': 'app.title.discover',
    '/discover/series/:tmdbId': 'app.title.discover',
    '/requests': 'app.title.requests',
    '/movies': 'app.title.movies',
    '/movies/:id': 'app.title.movies',
    '/movies/:id/search': 'app.title.interactiveSearch',
    '/series': 'app.title.series',
    '/series/:id': 'app.title.series',
    '/series/:id/search': 'app.title.interactiveSearch',
    '/series/:id/search/:season': 'app.title.interactiveSearch',
    '/series/:id/search/:season/:episode': 'app.title.interactiveSearch',
    '/anime': 'app.title.anime',
    '/l/:slug': 'app.title.library',
    '/adult': 'app.title.adult',
    '/adult/scenes': 'app.title.adult',
    '/adult/scenes/:provider/:stashId': 'app.title.adult',
    '/adult/sites/:id': 'app.title.adult',
    '/adult/sites/:id/search': 'app.title.interactiveSearch',
    '/adult/sites/:id/search/:year': 'app.title.interactiveSearch',
    '/adult/sites/:id/search/:year/:number': 'app.title.interactiveSearch',
    '/search': 'app.title.search',
    '/queue': 'app.title.queue',
    '/convert': 'app.title.convert',
    '/wanted': 'app.title.wanted',
    '/calendar': 'app.title.calendar',
    '/history': 'app.title.history',
    '/scan-review': 'app.title.scanReview',
    '/settings': 'app.title.settings',
    '/settings/:section': 'app.title.settings',
  };

  let sidebarOpen = $state(false);
  let sidebarMenuButton = $state<HTMLButtonElement | undefined>(undefined);

  let addOpen = $state(false);
  let addKind = $state<'movie' | 'series' | 'anime'>('movie');

  function closeSidebar() {
    if (!sidebarOpen) return;
    sidebarOpen = false;
    sidebarMenuButton?.focus();
  }

  function toggleSidebar() {
    if (sidebarOpen) {
      closeSidebar();
      return;
    }
    sidebarOpen = true;
  }

  let sidebarPath = router.path;
  $effect(() => {
    const path = router.path;
    if (path === sidebarPath) return;
    sidebarPath = path;
    closeSidebar();
  });


  function openAdd(kind: 'movie' | 'series' | 'anime') {
    addKind = kind;
    addOpen = true;
  }

  /**
   * The shelf the reader is standing on, for the add dialog's opening tab.
   *
   * It names a KIND, not a library: the dialog turns it into that kind's
   * default shelf, and a `?library=` on the URL — which is what every sidebar
   * shelf row links with — is more specific and wins there.
   */
  function shelfAddKind(): 'movie' | 'series' | 'anime' {
    if (router.match?.pattern === '/l/:slug') {
      const lib = sessionLibraryBySlug(session.user, router.match.params.slug ?? '');
      if (lib?.kind === 'tv') return 'series';
      if (lib?.kind === 'anime') return 'anime';
      return 'movie';
    }
    if (router.path.startsWith('/series')) return 'series';
    if (router.path.startsWith('/anime')) return 'anime';
    return 'movie';
  }

  /**
   * The "no password on a public bind" nag (SPEC §11). Dismissing it is
   * per-session on purpose: it is a real risk, so it comes back on the next
   * visit, but it must not nag on every navigation of the session you already
   * decided about.
   */
  const NAG_KEY = 'caravan.public-bind-nag-dismissed';
  let nagDismissed = $state(readNagDismissed());

  function readNagDismissed(): boolean {
    try {
      return window.sessionStorage.getItem(NAG_KEY) === '1';
    } catch {
      // Private mode, or storage disabled: nagging every load is the safe side.
      return false;
    }
  }

  function dismissNag() {
    nagDismissed = true;
    try {
      window.sessionStorage.setItem(NAG_KEY, '1');
    } catch {
      // The in-memory flag is enough for this session.
    }
  }

  let unreachableClients = $derived(
    unreachableClientBanner(system.status?.unhealthy_download_clients),
  );

  let unreachableStash = $derived(stashUnreachableBanner(system.status?.stash_unreachable));

  let showBindNag = $derived(
    !nagDismissed &&
      system.status?.listening_publicly === true &&
      system.status?.password_set !== true,
  );

  onMount(() => {
    const stop = startRouter();
    void boot();
    return stop;
  });

  /**
   * Who we are, then what the server is doing. The order matters: GET
   * /system/status is an admin route, so asking for it as a member would turn a
   * perfectly healthy server into a "Caravan server unreachable" banner.
   */
  async function boot() {
    await session.refresh();
    if (session.isAdmin) await system.refresh();
  }

  // Admin badges stay live through SSE invalidation, not a 60s poll.
  // Members do not get the stream: it is admin-only, and their only
  // badge (requests) updates from the request they just wrote.
  $effect(() => {
    if (!session.isAdmin) return;
    return startLiveUpdates();
  });

  // Route gate: no storage root means first run, and once there is one the
  // first-run screen is no longer reachable (SPEC §10.1).
  $effect(() => {
    // A missing session says nothing about setup state: the login screen owns
    // the whole viewport until it is resolved.
    if (auth.required) return;
    // Setup is an admin's job, and /first-run is not a member route: if this
    // gate pushed a member there, the member guard below would push them
    // straight back out, and the two effects would chase each other forever.
    // A member on an unconfigured server just gets an empty Discover.
    if (!session.isAdmin) return;
    if (system.loading) return;
    if (system.needsSetup) {
      if (router.path !== '/first-run') navigate('/first-run', { replace: true });
      return;
    }
    // The index forwards to Discover below. Finished first-run lands there
    // so the brand mark and a completed setup share one home.
    if (router.path === '/first-run') {
      navigate('/', { replace: true });
    }
  });

  /**
   * `/` is the brand target. Discover is the primary screen, so the index
   * only exists long enough to send the reader there. The Discover nav row
   * stays at `/discover` so a reload and a bookmark share one URL.
   *
   * An unconfigured admin is still first-run's: forwarding here while setup
   * is loading or still needed would race that gate.
   */
  $effect(() => {
    if (auth.required) return;
    if (router.path !== '/') return;
    if (session.isAdmin && (system.loading || system.needsSetup)) return;
    navigate('/discover', { replace: true });
  });

  /**
   * Members have no library, activity or system screens. A direct link to one —
   * a bookmark, a shared URL, a role that changed under them — lands on
   * Discover rather than on a screen whose every call 403s.
   *
   * The server is still the enforcer; this only spares them the wreckage.
   */
  $effect(() => {
    if (session.isAdmin) return;
    const current = router.match;
    if (current && memberAllowedRoute(current.pattern)) return;
    navigate('/discover', { replace: true });
  });

  /**
   * The adult module's screens exist only for an account it is visible to — the
   * server-wide switch on AND this account granted (an admin is implicitly
   * granted). Every one of those routes 404s otherwise, so a bookmark, a shared
   * link, or a grant that was revoked under an open tab lands on the shelf the
   * reader does have rather than on a screen whose every call answers "no such
   * path".
   *
   * It waits for /auth/me: `session.adult` is false while the answer is in
   * flight, and redirecting on that would bounce an admin off their own
   * bookmark on every boot. This is the opposite of the member guard's
   * treatment of an unknown identity, and deliberately so — that one guesses
   * generously because a wrong guess costs a 403 screen, and this one refuses
   * to guess at all because a wrong guess costs the phase's whole promise.
   */
  $effect(() => {
    if (session.loading) return;
    if (session.adult) return;
    const current = router.match;
    if (!current || !isAdultRoute(current.pattern)) return;
    navigate(session.isAdmin ? '/movies' : '/discover', { replace: true });
  });

  /**
   * The retired Scenes tab. Its job is Explore's adult scope now, and an old
   * bookmark lands there rather than on Not found.
   *
   * It is gated on the grant for the same reason the effect above exists: an
   * ungranted reader must be sent to their own shelf, not forwarded to another
   * adult URL. Checking `session.adult` here is what keeps the two effects from
   * chasing each other — for an ungranted reader only the one above fires, and
   * for a granted one only this.
   */
  $effect(() => {
    if (session.loading || !session.adult) return;
    if (router.match?.pattern !== '/adult/scenes') return;
    navigate(ADULT_EXPLORE_HREF, { replace: true });
  });

  /**
   * Adult stays a module on /adult, not a per-library shelf. A slug that
   * resolves to an adult library is a bookmark alias and lands on the module.
   */
  $effect(() => {
    if (session.loading) return;
    if (router.match?.pattern !== '/l/:slug') return;
    const lib = sessionLibraryBySlug(session.user, router.match.params.slug ?? '');
    if (lib?.kind !== 'adult') return;
    navigate('/adult', { replace: true });
  });

  function onKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape' && sidebarOpen) {
      event.preventDefault();
      closeSidebar();
      return;
    }

    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
      if (settingsSection !== undefined) {
        event.preventDefault();
        document.getElementById('settings-search')?.focus();
        return;
      }

      // Adding straight to the library is an admin's to do; a member's ⌘K
      // would open a dialog whose submit is a 403.
      // The dialog seeds the tab of the shelf you are standing on.
      if (!session.isAdmin) return;
      event.preventDefault();
      openAdd(shelfAddKind());
    }
  }

  let match = $derived(router.match);
  let settingsSection = $derived.by(() => {
    if (!match || (match.pattern !== '/settings' && match.pattern !== '/settings/:section')) {
      return undefined;
    }
    return match.params.section ?? '';
  });
  let title = $derived.by(() => {
    if (!match) return t('app.title.notFound');
    if (match.pattern === '/l/:slug') {
      return (
        sessionLibraryBySlug(session.user, match.params.slug ?? '')?.name ?? t('app.title.library')
      );
    }
    return t(TITLES[match.pattern]);
  });
  let document_title = $derived(match ? `${title} · Caravan` : 'Caravan');
</script>

<svelte:window onkeydown={onKeydown} />
<svelte:head>
  <title>{document_title}</title>
</svelte:head>

{#if shutdown.phase === 'stopped'}
  <!-- Terminal, and first: the server is gone, so every other screen would
       only be rendering stale data behind failing polls (SPEC §2.3). Unmounting
       the shell is also what stops those polls. -->
  <SafeToEject />
{:else if auth.required}
  <Login />
{:else if router.path === '/first-run'}
  <FirstRun />
{:else}
  <div class="flex h-full">
    <Sidebar open={sidebarOpen} onclose={closeSidebar} {settingsSection} />

    <!-- `relative` makes this column the containing block for absolutely
         positioned descendants (every `sr-only` label is one). Without it
         they anchor to <body>, and one sitting below the fold extends the
         document's scrollable area: the whole shell, sidebar included,
         scrolls behind this column's own scrollbar. -->
    <div class="relative flex min-w-0 flex-1 flex-col overflow-x-hidden overflow-y-auto">
      <TopBar
        {title}
        onadd={
          settingsSection === undefined && session.isAdmin
            ? () => openAdd(shelfAddKind())
            : undefined
        }
        onmenu={toggleSidebar}
        menuOpen={sidebarOpen}
        bind:menuButton={sidebarMenuButton} />

      <!-- min-w-0 so a wide child (Discover's poster row) cannot raise this
           column's automatic minimum and turn the page into the scroller.
           overflow-x lives on the child that owns the row. -->
      <main class="flex min-w-0 flex-1 flex-col gap-4 px-6 py-6">
        <!-- Every banner here reports on the server itself, and every one of
             them offers a fix on a screen a member cannot open. They are also
             read from a status a member is never allowed to fetch (see boot). -->
        {#if session.isAdmin}
          {#if system.error}
            <Banner
              tone="danger"
              icon="warning"
              title={t('app.banner.serverUnreachable')}
              message={system.error} />
          {:else if system.status?.dirty}
            <DirtyRecovery />
          {/if}

          <!-- One client being down is not the system being down, so this sits
               below the server/dirty banners and names the client (SPEC §5.1). -->
          {#if unreachableClients}
            <Banner
              tone="warning"
              icon="warning"
              title={unreachableClients.title}
              message={unreachableClients.message} />
          {/if}

          <!-- The same shape for the adult library's handoff. The field is only
               on the payload for a caller the module is visible to, so there is
               no session check here. -->
          {#if unreachableStash}
            <Banner
              tone="warning"
              icon="warning"
              title={unreachableStash.title}
              message={unreachableStash.message} />
          {/if}

          {#if showBindNag}
            <Banner
              tone="warning"
              icon="warning"
              title={t('app.banner.publicBind.title')}
              message={t('app.banner.publicBind.message')}>
              {#snippet action()}
                <div class="flex items-center gap-3">
                  <a href="/settings/users" class="text-sm font-semibold text-accent-text hover:underline">
                    {t('app.banner.publicBind.settingsLink')}
                  </a>
                  <Button variant="secondary" size="sm" onclick={dismissNag}>
                    {t('common.dismiss')}
                  </Button>
                </div>
              {/snippet}
            </Banner>
          {/if}
        {/if}

        {#if !match}
          <NotFound />
        {:else if match.pattern === '/' || match.pattern === '/discover'}
          <Discover />
        {:else if match.pattern === '/discover/movies' || match.pattern === '/discover/series'}
          <!-- Keyed on the PATH, not the query string: a scope switch is a new
               screen and starts empty, while a filter change is the same screen
               asking a narrower question and must not tear down its grid. -->
          {#key match.pattern}
            <ExploreTitles mediaType={match.pattern === '/discover/movies' ? 'movie' : 'series'} />
          {/key}
          <!-- Gated on the grant for the RENDER as well as the redirect, exactly
               as the /adult screens below are: the guard effect runs after the
               DOM is updated, so without this an ungranted browser would mount
               the scope for one tick and put a request to /adult/discover on the
               wire before being sent away. -->
        {:else if session.adult && match.pattern === '/discover/adult'}
          <ExploreAdult />
        {:else if match.pattern === '/discover/network/:id' || match.pattern === '/discover/studio/:id'}
          {#key router.path}
            <DiscoverBrowse
              type={match.pattern === '/discover/network/:id' ? 'network' : 'studio'}
              id={numericParam(match.params, 'id')} />
          {/key}
        {:else if match.pattern === '/discover/movie/:tmdbId' || match.pattern === '/discover/series/:tmdbId'}
          {#key router.path}
            <DiscoverTitle
              type={match.pattern === '/discover/movie/:tmdbId' ? 'movie' : 'series'}
              tmdbID={numericParam(match.params, 'tmdbId')} />
          {/key}
        {:else if match.pattern === '/requests'}
          <Requests />
        {:else if match.pattern === '/movies'}
          <Movies onadd={() => openAdd('movie')} />
        {:else if match.pattern === '/movies/:id'}
          {#key match.params.id}
            <MovieDetail id={numericParam(match.params, 'id')} />
          {/key}
        {:else if match.pattern === '/movies/:id/search'}
          {#key router.path}
            <ReleaseSearch kind="movie" id={numericParam(match.params, 'id')} />
          {/key}
        {:else if match.pattern === '/anime'}
          <!-- The add opens on the Movies tab, as it does everywhere but the
               series shelf: an anime library accepts both, so the tab is a
               starting point rather than a decision. -->
          <Anime onadd={() => openAdd('anime')} />
        {:else if match.pattern === '/l/:slug'}
          {@const lib = sessionLibraryBySlug(session.user, match.params.slug ?? '')}
          {#if !lib}
            <NotFound />
          {:else if lib.kind === 'adult'}
            {#if session.adult}
              <Adult />
            {/if}
          {:else if lib.kind === 'tv'}
            <Series onadd={() => openAdd('series')} libraryId={lib.id} />
          {:else if lib.kind === 'anime'}
            <Anime onadd={() => openAdd('anime')} libraryId={lib.id} />
          {:else}
            <Movies onadd={() => openAdd('movie')} libraryId={lib.id} />
          {/if}
        {:else if match.pattern === '/series'}
          <Series onadd={() => openAdd('series')} />
        {:else if match.pattern === '/series/:id'}
          {#key match.params.id}
            <SeriesDetail id={numericParam(match.params, 'id')} />
          {/key}
        {:else if match.pattern === '/series/:id/search'}
          {#key router.path}
            <!-- No season: ReleaseSearch reads -1 as "search the whole series". -->
            <ReleaseSearch kind="series" id={numericParam(match.params, 'id')} />
          {/key}
        {:else if match.pattern === '/series/:id/search/:season'}
          {#key router.path}
            <ReleaseSearch
              kind="series"
              id={numericParam(match.params, 'id')}
              season={ordinalParam(match.params, 'season')} />
          {/key}
        {:else if match.pattern === '/series/:id/search/:season/:episode'}
          {#key router.path}
            <ReleaseSearch
              kind="series"
              id={numericParam(match.params, 'id')}
              season={ordinalParam(match.params, 'season')}
              episode={ordinalParam(match.params, 'episode')} />
          {/key}
          <!-- `session.adult` gates the RENDER as well as the redirect above.
               The guard effect runs after the DOM is updated, so without this
               an ungranted browser would mount the screen for one tick and put
               a request to /adult/sites on the wire before being sent away —
               a trace, from a browser that is supposed to have none. -->
        {:else if session.adult && match.pattern === '/adult'}
          <Adult />
        {:else if session.adult && match.pattern === '/adult/sites/:id'}
          {#key match.params.id}
            <AdultSite id={numericParam(match.params, 'id')} />
          {/key}
        {:else if session.adult && match.pattern === '/adult/scenes/:provider/:stashId'}
          {#key router.path}
            <AdultScene provider={match.params.provider} stashID={match.params.stashId} />
          {/key}
          <!-- The picker is gated on isAdmin as well as on `session.adult`, and
               for the same reason the adult routes are gated on render at all:
               the member redirect below runs after the DOM is updated, so a
               granted member would otherwise mount the screen for one tick and
               put a release search for somebody else's site on the wire before
               being sent away. Grabbing is an admin write; the server refuses
               it, and this refuses to ask. -->
        {:else if session.adult && session.isAdmin && match.pattern === '/adult/sites/:id/search'}
          {#key router.path}
            <!-- No year: ReleaseSearch reads -1 as "search the whole site". -->
            <ReleaseSearch kind="site" id={numericParam(match.params, 'id')} />
          {/key}
        {:else if session.adult && session.isAdmin && match.pattern === '/adult/sites/:id/search/:year'}
          {#key router.path}
            <ReleaseSearch
              kind="site"
              id={numericParam(match.params, 'id')}
              season={ordinalParam(match.params, 'year')} />
          {/key}
        {:else if session.adult && session.isAdmin && match.pattern === '/adult/sites/:id/search/:year/:number'}
          {#key router.path}
            <ReleaseSearch
              kind="site"
              id={numericParam(match.params, 'id')}
              season={ordinalParam(match.params, 'year')}
              episode={ordinalParam(match.params, 'number')} />
          {/key}
        {:else if match.pattern === '/search'}
          <Search />
        {:else if match.pattern === '/queue'}
          <Queue />
        {:else if match.pattern === '/convert'}
          <Convert />
        {:else if match.pattern === '/wanted'}
          <Wanted />
        {:else if match.pattern === '/calendar'}
          <Calendar />
        {:else if match.pattern === '/history'}
          <History />
        {:else if match.pattern === '/scan-review'}
          <ScanReview />
        {:else if match.pattern === '/settings' || match.pattern === '/settings/:section'}
          <SettingsScreen section={match.params.section ?? ''} />
        {/if}
      </main>
    </div>
  </div>
{/if}

{#if addOpen}
  <AddItemModal initialKind={addKind} onclose={() => (addOpen = false)} />
{/if}

<Toasts />
