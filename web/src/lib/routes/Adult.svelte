<script lang="ts">
  /**
   * Library → Adult: the site grid (PLAN phase 9 task 7c).
   *
   * It is the Series screen's grid with different nouns — a site is a series
   * row, a scene is an episode row — so it reuses PosterGrid/PosterCard and the
   * shared status vocabulary rather than inventing a second card. What it does
   * NOT reuse is the Series screen's data: GET /library/series is television
   * only by contract, and GET /adult/sites is the door to this shelf.
   *
   * There is no member/admin branch on reading. A granted member reaches this
   * screen (the server's own allowlist names it), and the gate that decides
   * whether it exists at all is the router's, not this component's — by the
   * time this mounts, `session.adult` is already true.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Site } from '../api/types';
  import AddItemModal from '../components/AddItemModal.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import PosterCard from '../components/PosterCard.svelte';
  import PosterGrid from '../components/PosterGrid.svelte';
  import PosterGridSkeleton from '../components/PosterGridSkeleton.svelte';
  import PageTabs from '../components/PageTabs.svelte';
  import TextInput from '../components/TextInput.svelte';
  import { ADULT_TABS, adultTabHref, sceneCountNote, siteHref } from '../adult';
  import { navigate } from '../router.svelte';
  import { session } from '../state/session.svelte';
  import type { StatusKey } from '../status';

  let sites = $state<Site[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let query = $state('');

  /** The add-a-site picker. Admin-only: POST /adult/sites answers 403 to a member. */
  let picking = $state(false);

  async function load() {
    loading = true;
    try {
      sites = await api.listSites();
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  let all = $derived(sites ?? []);

  let visible = $derived.by(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return all;
    return all.filter((site) => site.title.toLowerCase().includes(needle));
  });

  /**
   * A site's status in the shared vocabulary. It is the series rule with the
   * scene counts substituted, and it is spelled out rather than calling
   * `seriesStatus` because a Site is not a Series — the counts have different
   * names, and passing a cast object would be the kind of lie that survives a
   * later field change.
   */
  function siteStatus(site: Site): StatusKey {
    if (site.scene_count > 0 && site.scene_file_count >= site.scene_count) return 'downloaded';
    if (site.scene_file_count > 0) return 'incomplete';
    if (!site.monitored) return 'unmonitored';
    return 'wanted';
  }

  // The picker is the ⌘K add dialog fixed to its adult scope, and it is mounted
  // only while it is open: its search starts on mount (a blank query is the
  // endpoint's default list), so a picker that existed all the time would ask
  // the provider for one on every visit to this tab. Adding navigates to the new
  // site, so this screen has nothing to reload afterwards.
</script>

<div class="flex flex-col gap-6">
  <PageTabs
    tabs={ADULT_TABS}
    active="sites"
    ariaLabel="Adult sections"
    onchange={(key) => navigate(adultTabHref(key))} />

  <div class="flex flex-wrap items-center gap-3">
    <div class="ml-auto flex items-center gap-2">
      <div class="w-56">
        <TextInput
          bind:value={query}
          type="search"
          placeholder="Filter sites…"
          ariaLabel="Filter sites by name" />
      </div>
      <Button variant="secondary" onclick={load} title="Reload the site list">
        <Icon name="refresh" size={14} />
        Refresh
      </Button>
      {#if session.isAdmin}
        <Button variant="primary" onclick={() => (picking = true)}>
          <Icon name="plus" size={14} />
          Add site
        </Button>
      {/if}
    </div>
  </div>

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && sites === null}
    <PosterGridSkeleton />
  {:else if all.length === 0}
    <EmptyState
      icon="flame"
      title="No sites yet"
      message={session.isAdmin
        ? 'Add a site and Caravan walks its whole catalogue, filing each scene under its release year.'
        : 'Nothing has been added to this shelf yet. Ask for a scene from Scenes and it shows up here once it is approved.'}>
      {#snippet action()}
        {#if session.isAdmin}
          <Button variant="primary" onclick={() => (picking = true)}>
            <Icon name="plus" size={14} />
            Add site
          </Button>
        {:else}
          <Button variant="primary" href="/adult/scenes">Browse scenes</Button>
        {/if}
      {/snippet}
    </EmptyState>
  {:else if visible.length === 0}
    <EmptyState
      icon="search"
      title="Nothing matches this filter"
      message="No site on this shelf matches the current search.">
      {#snippet action()}
        <Button variant="secondary" onclick={() => (query = '')}>Clear filter</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <PosterGrid>
      {#each visible as site (site.id)}
        <PosterCard
          href={siteHref(site)}
          title={site.title}
          year={0}
          posterPath={site.poster_path}
          posterUrl={site.poster_url}
          status={siteStatus(site)}
          note={sceneCountNote(site)}
          fallbackIcon="flame" />
      {/each}
    </PosterGrid>
  {/if}
</div>

{#if picking}
  <AddItemModal initialKind="site" onclose={() => (picking = false)} />
{/if}
