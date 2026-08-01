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
  import Toasts from './lib/components/Toasts.svelte';
  import Sidebar from './lib/layout/Sidebar.svelte';
  import TopBar from './lib/layout/TopBar.svelte';
  import { numericParam, type RoutePattern } from './lib/router';
  import { navigate, router, startRouter } from './lib/router.svelte';
  import FirstRun from './lib/routes/FirstRun.svelte';
  import MovieDetail from './lib/routes/MovieDetail.svelte';
  import Movies from './lib/routes/Movies.svelte';
  import NotFound from './lib/routes/NotFound.svelte';
  import ScanReview from './lib/routes/ScanReview.svelte';
  import Series from './lib/routes/Series.svelte';
  import SeriesDetail from './lib/routes/SeriesDetail.svelte';
  import SettingsScreen from './lib/routes/Settings.svelte';
  import { system } from './lib/state/system.svelte';

  const TITLES: Record<RoutePattern, string> = {
    '/first-run': 'Welcome',
    '/movies': 'Movies',
    '/movies/:id': 'Movies',
    '/series': 'Series',
    '/series/:id': 'Series',
    '/scan-review': 'Scan Review',
    '/settings': 'Settings',
  };

  let addOpen = $state(false);

  onMount(() => {
    const stop = startRouter();
    system.refresh();
    return stop;
  });

  // Route gate: no storage root means first run, and once there is one the
  // first-run screen is no longer reachable (SPEC §10.1).
  $effect(() => {
    if (system.loading) return;
    if (system.needsSetup) {
      if (router.path !== '/first-run') navigate('/first-run', { replace: true });
      return;
    }
    if (router.path === '/first-run' || router.path === '/') {
      navigate('/movies', { replace: true });
    }
  });

  function onKeydown(event: KeyboardEvent) {
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
      event.preventDefault();
      addOpen = true;
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

{#if router.path === '/first-run'}
  <FirstRun />
{:else}
  <div class="flex h-full">
    <Sidebar />

    <div class="flex min-w-0 flex-1 flex-col overflow-y-auto">
      <TopBar {title} onsearch={() => (addOpen = true)} />

      <main class="flex flex-1 flex-col gap-4 px-6 py-6">
        {#if system.error}
          <Banner
            tone="danger"
            icon="warning"
            title="Caravan server unreachable"
            message={system.error} />
        {:else if system.status?.dirty}
          <Banner
            tone="danger"
            icon="warning"
            title="Last shutdown was not clean"
            message="Caravan detected an unsafe shutdown. Verify the filesystem and run a library scan before trusting the database." />
        {/if}

        {#if !match}
          <NotFound />
        {:else if match.pattern === '/movies'}
          <Movies onadd={() => (addOpen = true)} />
        {:else if match.pattern === '/movies/:id'}
          {#key match.params.id}
            <MovieDetail id={numericParam(match.params, 'id')} />
          {/key}
        {:else if match.pattern === '/series'}
          <Series onadd={() => (addOpen = true)} />
        {:else if match.pattern === '/series/:id'}
          {#key match.params.id}
            <SeriesDetail id={numericParam(match.params, 'id')} />
          {/key}
        {:else if match.pattern === '/scan-review'}
          <ScanReview />
        {:else if match.pattern === '/settings'}
          <SettingsScreen />
        {/if}
      </main>
    </div>
  </div>
{/if}

{#if addOpen}
  <AddItemModal onclose={() => (addOpen = false)} />
{/if}

<Toasts />
