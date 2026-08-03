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
    memberAllowedRoute,
    numericParam,
    ordinalParam,
    type RoutePattern,
  } from './lib/router';
  import { navigate, router, startRouter } from './lib/router.svelte';
  import Calendar from './lib/routes/Calendar.svelte';
  import Convert from './lib/routes/Convert.svelte';
  import Discover from './lib/routes/Discover.svelte';
  import DiscoverBrowse from './lib/routes/DiscoverBrowse.svelte';
  import DiscoverTitle from './lib/routes/DiscoverTitle.svelte';
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
  import ScanReview from './lib/routes/ScanReview.svelte';
  import Series from './lib/routes/Series.svelte';
  import SeriesDetail from './lib/routes/SeriesDetail.svelte';
  import Wanted from './lib/routes/Wanted.svelte';
  import SettingsScreen from './lib/routes/Settings.svelte';
  import Button from './lib/components/Button.svelte';
  import { unreachableClientBanner } from './lib/download';
  import { auth } from './lib/state/auth.svelte';
  import { session } from './lib/state/session.svelte';
  import { shutdown } from './lib/state/shutdown.svelte';
  import { system } from './lib/state/system.svelte';

  const TITLES: Record<RoutePattern, string> = {
    '/first-run': 'Welcome',
    '/': 'Discover',
    '/discover': 'Discover',
    '/discover/network/:id': 'Discover',
    '/discover/studio/:id': 'Discover',
    '/discover/movie/:tmdbId': 'Discover',
    '/discover/series/:tmdbId': 'Discover',
    '/requests': 'Requests',
    '/movies': 'Movies',
    '/movies/:id': 'Movies',
    '/movies/:id/search': 'Interactive Search',
    '/series': 'Series',
    '/series/:id': 'Series',
    '/series/:id/search': 'Interactive Search',
    '/series/:id/search/:season': 'Interactive Search',
    '/series/:id/search/:season/:episode': 'Interactive Search',
    '/queue': 'Queue',
    '/convert': 'Convert',
    '/wanted': 'Wanted',
    '/calendar': 'Calendar',
    '/history': 'History',
    '/scan-review': 'Scan Review',
    '/settings': 'Settings',
    '/settings/:section': 'Settings',
  };

  let addOpen = $state(false);
  let addKind = $state<'movie' | 'series'>('movie');

  function openAdd(kind: 'movie' | 'series' = 'movie') {
    addKind = kind;
    addOpen = true;
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

  // Route gate: no storage root means first run, and once there is one the
  // first-run screen is no longer reachable (SPEC §10.1).
  $effect(() => {
    // A missing session says nothing about setup state: the login screen owns
    // the whole viewport until it is resolved.
    if (auth.required) return;
    if (system.loading) return;
    if (system.needsSetup) {
      if (router.path !== '/first-run') navigate('/first-run', { replace: true });
      return;
    }
    // `/` is Discover now, so only the finished first-run screen is redirected.
    if (router.path === '/first-run') {
      navigate('/', { replace: true });
    }
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

  function onKeydown(event: KeyboardEvent) {
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
      // Adding straight to the library is an admin's to do; a member's ⌘K
      // would open a dialog whose submit is a 403.
      if (!session.isAdmin) return;
      event.preventDefault();
      openAdd();
    }
  }

  let match = $derived(router.match);
  let title = $derived(match ? TITLES[match.pattern] : 'Not found');
  let document_title = $derived(match ? `${TITLES[match.pattern]} · Caravan` : 'Caravan');
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
    <Sidebar />

    <div class="flex min-w-0 flex-1 flex-col overflow-y-auto">
      <TopBar {title} onsearch={session.isAdmin ? () => openAdd() : undefined} />

      <main class="flex flex-1 flex-col gap-4 px-6 py-6">
        <!-- Every banner here reports on the server itself, and every one of
             them offers a fix on a screen a member cannot open. They are also
             read from a status a member is never allowed to fetch (see boot). -->
        {#if session.isAdmin}
          {#if system.error}
            <Banner
              tone="danger"
              icon="warning"
              title="Caravan server unreachable"
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

          {#if showBindNag}
            <Banner
              tone="warning"
              icon="warning"
              title="Listening on every interface without a password"
              message="Anyone on this network can reach Caravan and change its settings. Add an account under Settings → Users.">
              {#snippet action()}
                <Button variant="secondary" size="sm" onclick={dismissNag}>Dismiss</Button>
              {/snippet}
            </Banner>
          {/if}
        {/if}

        {#if !match}
          <NotFound />
        {:else if match.pattern === '/' || match.pattern === '/discover'}
          <Discover />
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
