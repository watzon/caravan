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
  import Dropdown from '../components/Dropdown.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import PosterCard from '../components/PosterCard.svelte';
  import PosterGrid from '../components/PosterGrid.svelte';
  import PosterGridSkeleton from '../components/PosterGridSkeleton.svelte';
  import SelectActions from '../components/SelectActions.svelte';
  import TextInput from '../components/TextInput.svelte';
  import { ADULT_EXPLORE_HREF, sceneCountNote, siteHref } from '../adult';
  import { createSelection } from '../selection.svelte';
  import { session } from '../state/session.svelte';
  import { navigate, router } from '../router.svelte';
  import { SERIES_FILTERS, type StatusKey } from '../status';
  import { useI18n } from '../i18n.svelte';

  const { t } = useI18n();


  type SortKey = 'title' | 'added' | 'status';

  const SORT_OPTIONS: { key: SortKey; label: string }[] = [
    { key: 'title', label: t('route.adult.sortTitle') },
    { key: 'added', label: t('route.adult.sortAdded') },
    { key: 'status', label: t('route.adult.sortStatus') },
  ];

  /** The dropdown takes {id, name}; the rail's order is the array's. */
  const SORT_CHOICES = SORT_OPTIONS.map((option) => ({ id: option.key, name: option.label }));

  function readSort(value: string | null): SortKey {
    return value === 'added' || value === 'status' ? value : 'title';
  }

  let sites = $state<Site[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let query = $state('');
  let sort = $derived(readSort(router.params.get('sort')));

  /** The add-a-site picker. Admin-only: POST /adult/sites answers 403 to a member. */
  let picking = $state(false);

  /**
   * Grid selection, which is the Series shelf's and does the same three things
   * to the same routes — a site is a series row, so searching, monitoring and
   * removing one in bulk is the same call per id.
   *
   * It exists for an admin only: every action behind it is a write a member's
   * session would be refused, and a selection whose action bar 403s is worse
   * than no selection at all.
   */
  const selection = createSelection();

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
    const filtered = needle
      ? all.filter((site) => site.title.toLowerCase().includes(needle))
      : all;
    return [...filtered].sort(compareSites);
  });

  function compareTitle(a: Site, b: Site): number {
    return (
      (a.sort_title || a.title).localeCompare(b.sort_title || b.title) ||
      a.title.localeCompare(b.title) ||
      a.id - b.id
    );
  }

  function compareSites(a: Site, b: Site): number {
    if (sort === 'added') {
      return b.added_at.localeCompare(a.added_at) || compareTitle(a, b);
    }
    if (sort === 'status') {
      return (
        SERIES_FILTERS.indexOf(siteStatus(a)) - SERIES_FILTERS.indexOf(siteStatus(b)) ||
        compareTitle(a, b)
      );
    }
    return compareTitle(a, b);
  }

  function applySort(value: string) {
    const next = readSort(value);
    const params = router.params;
    if (next === 'title') params.delete('sort');
    else params.set('sort', next);
    const search = params.toString();
    navigate(`${router.path}${search ? `?${search}` : ''}${router.hash}`);
  }

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
  <!-- No tab strip: the Scenes tab was retired in phase 12 and its job moved
       to Explore's adult scope, so this shelf is sites and only sites. A strip
       with one tab in it is a strip that says nothing. -->
  <div class="flex flex-wrap items-center gap-3">
    <div class="ml-auto flex w-full flex-wrap items-center justify-end gap-2 sm:w-auto">
      <Dropdown
        label={t('route.adult.sort')}
        options={SORT_CHOICES}
        value={sort}
        onselect={applySort}
        shape="box" />
      <div class="w-full sm:w-56">
        <TextInput
          bind:value={query}
          type="search"
          placeholder={t('route.adult.filterPlaceholder')}
          ariaLabel={t('route.adult.filterAria')} />
      </div>
      <!-- Ghost-icon refresh: a utility, not a destination. The add button
           stays: the top bar's global add has no adult scope, so this is the
           one way in. -->
      <Button variant="ghost" onclick={load} title={t('route.adult.reloadTitle')} class="px-2">
        <Icon name="refresh" size={14} />
        <span class="sr-only">{t('route.adult.refresh')}</span>
      </Button>
      {#if session.isAdmin}
        <Button variant="primary" onclick={() => (picking = true)}>
          <Icon name="plus" size={14} />
          {t('route.adult.addSite')}
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
      title={t('route.adult.emptyTitle')}
      message={session.isAdmin ? t('route.adult.emptyAdmin') : t('route.adult.emptyMember')}>
      {#snippet action()}
        {#if session.isAdmin}
          <Button variant="primary" onclick={() => (picking = true)}>
            <Icon name="plus" size={14} />
            {t('route.adult.addSite')}
          </Button>
        {:else}
          <Button variant="primary" href={ADULT_EXPLORE_HREF}>{t('route.adult.browseScenes')}</Button>
        {/if}
      {/snippet}
    </EmptyState>
  {:else if visible.length === 0}
    <EmptyState
      icon="search"
      title={t('route.adult.noFilterTitle')}
      message={t('route.adult.noFilterMessage')}>
      {#snippet action()}
        <Button variant="secondary" onclick={() => (query = '')}>{t('route.adult.clearFilter')}</Button>
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
          fallbackIcon="flame"
          posterFit="contain"
          posterAspect="video"
          selectable={selection.active}
          selected={selection.has(site.id)}
          ontoggle={session.isAdmin ? () => selection.toggle(site.id) : undefined} />
      {/each}
    </PosterGrid>
  {/if}

  {#if session.isAdmin}
    <SelectActions
      {selection}
      noun="site"
      plural="sites"
      actions={{
        search: (id) => api.searchSeriesNow(id),
        setMonitored: (id, monitored) => api.setSeriesMonitored(id, monitored),
        remove: (id, deleteFiles) => api.deleteSeries(id, deleteFiles),
      }}
      onchanged={load} />
  {/if}
</div>

{#if picking}
  <AddItemModal initialKind="site" onclose={() => (picking = false)} />
{/if}
