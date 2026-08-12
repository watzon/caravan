<script lang="ts">
  /** Adult-only exact-scene picker for Scan Review manual matches. */
  import { onMount, untrack } from 'svelte';
  import { EVERY_SCENE_FILTER, sceneFiltersOf } from '../adult';
  import { api, errorText } from '../api/client';
  import type {
    AdultDiscoverPage,
    SceneFilterSupport,
    SceneMeta,
    SiteMeta,
    StashboxInstance,
  } from '../api/types';
  import {
    SCENE_SITE_SCOPES,
    SCENE_SORTS,
    sceneApiQuery,
    type FilterRef,
    type SceneFilter,
  } from '../explore';
  import { formatDate } from '../format';
  import { useI18n } from '../i18n.svelte';
  import { session } from '../state/session.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { createTypeahead } from '../typeahead.svelte';
  import Badge from './Badge.svelte';
  import Banner from './Banner.svelte';
  import Button from './Button.svelte';
  import EmptyState from './EmptyState.svelte';
  import Field from './Field.svelte';
  import FilterTypeahead from './FilterTypeahead.svelte';
  import LoadError from './LoadError.svelte';
  import Modal from './Modal.svelte';
  import Poster from './Poster.svelte';
  import SearchSkeleton from './SearchSkeleton.svelte';
  import TextInput from './TextInput.svelte';

  interface Props {
    title: string;
    siteQuery: string;
    sceneDate: string;
    fallbackQuery?: string;
    providers?: string[];
    onpick: (scene: SceneMeta) => Promise<void> | void;
    onclose: () => void;
  }

  interface PickerResults extends AdultDiscoverPage {
    limited: boolean;
  }

  type SceneDateOp = 'on' | 'before' | 'on_or_before' | 'after' | 'on_or_after';

  const MAX_EXACT_PAGES = 20;
  const NO_SCENE_FILTER: SceneFilterSupport = {
    year: false,
    duration: false,
    site_scope: false,
    date_op: false,
    sort_duration: false,
    sort_relevance: false,
    any_of: false,
  };
  const SELECT_CLASS =
    'h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink ' +
    'focus:border-accent focus:outline-none';

  let {
    title,
    siteQuery,
    sceneDate,
    fallbackQuery = '',
    providers = [],
    onpick,
    onclose,
  }: Props = $props();
  const { t } = useI18n();

  let selectedProvider = $state(untrack(() => providers[0] ?? ''));
  let instances = $state<StashboxInstance[]>([]);
  let instancesLoaded = $state(false);
  let instancesError = $state<string | null>(null);
  let selectedSite = $state<SiteMeta | null>(null);
  let releaseDate = $state(untrack(() => sceneDate.slice(0, 10)));
  let dateOp = $state<SceneDateOp>('on');
  let scope = $state<'site' | 'parent' | 'network'>('site');
  let performers = $state<FilterRef[]>([]);
  let performersAll = $state(true);
  let tags = $state<FilterRef[]>([]);
  let tagsAll = $state(true);
  let year = $state(0);
  let duration = $state(0);
  let sort = $state('newest');
  let body = $state<HTMLElement | null>(null);
  let sceneList = $state<HTMLUListElement | null>(null);
  let busyKey = $state<string | null>(null);

  function blank(): PickerResults {
    return { page: 1, per_page: 0, total: 0, scenes: [], limited: false };
  }

  function siteKey(value: string): string {
    return value.normalize('NFKD').toLowerCase().replace(/[^a-z0-9]/g, '');
  }

  function exactSite(sites: SiteMeta[], query: string): SiteMeta | null {
    const wanted = siteKey(query);
    return sites.find((site) =>
      [site.name, ...(site.aliases ?? [])].some((name) => siteKey(name) === wanted)
    ) ?? null;
  }

  function providerInstance(id: string): StashboxInstance | undefined {
    return instances.find((instance) => instance.provider_id === id);
  }

  function providerLabel(id: string): string {
    return providerInstance(id)?.name ?? id;
  }

  let providerOptions = $derived(
    providers.length > 0
      ? providers.map((id) => ({ id, name: providerLabel(id) }))
      : [{ id: '', name: t('component.scenePicker.defaultProvider') }],
  );

  let support = $derived.by<SceneFilterSupport>(() => {
    if (!selectedProvider) return sceneFiltersOf(session.user);
    const instance = providerInstance(selectedProvider);
    if (!instance) return NO_SCENE_FILTER;
    return instance.scene_filters ?? (instancesLoaded ? EVERY_SCENE_FILTER : NO_SCENE_FILTER);
  });

  let sortOptions = $derived(
    SCENE_SORTS
      .filter((choice) => choice.key !== 'duration' || support.sort_duration)
      .filter((choice) => choice.key !== 'relevance' || support.sort_relevance)
      .map((choice) => ({ id: choice.key, name: choice.label })),
  );

  const dateOptions: { id: SceneDateOp; name: string }[] = [
    { id: 'on', get name() { return t('component.scenePicker.dateOn'); } },
    { id: 'before', get name() { return t('component.scenePicker.dateBefore'); } },
    { id: 'on_or_before', get name() { return t('component.scenePicker.dateOnOrBefore'); } },
    { id: 'after', get name() { return t('component.scenePicker.dateAfter'); } },
    { id: 'on_or_after', get name() { return t('component.scenePicker.dateOnOrAfter'); } },
  ];

  onMount(() => {
    void loadInstances();
  });

  async function loadInstances() {
    try {
      instances = await api.listStashboxInstances();
      instancesError = null;
    } catch (error) {
      instancesError = errorText(error);
    } finally {
      instancesLoaded = true;
    }
  }

  function changeProvider(provider: string) {
    selectedProvider = provider;
    selectedSite = null;
    performers = [];
    tags = [];
    siteSearch.query = siteQuery;
  }

  const siteSearch = createTypeahead<SiteMeta[]>({
    initial: untrack(() => siteQuery),
    blank: () => [],
    minQuery: 1,
    depends: () => [selectedProvider, session.adult],
    run: (query, signal) => {
      if (!session.adult) return Promise.resolve([]);
      return api.searchSites(query, signal, selectedProvider || undefined);
    },
  });

  $effect(() => {
    if (selectedSite || siteSearch.loading || siteSearch.trimmed === '') return;
    const site = exactSite(siteSearch.results, siteSearch.trimmed);
    if (site) selectedSite = site;
  });

  $effect(() => {
    if (!support.date_op && dateOp !== 'on') dateOp = 'on';
    if (!support.site_scope && scope !== 'site') scope = 'site';
    if (!support.any_of) {
      performersAll = true;
      tagsAll = true;
    }
    if (!support.year && year !== 0) year = 0;
    if (!support.duration && duration !== 0) duration = 0;
    if (!sortOptions.some((option) => option.id === sort)) sort = 'newest';
  });

  function changeSiteQuery(event: Event) {
    const value = (event.currentTarget as HTMLInputElement).value;
    if (selectedSite && siteKey(value) !== siteKey(selectedSite.name)) selectedSite = null;
  }

  function chooseSite(site: SiteMeta) {
    selectedSite = site;
    siteSearch.query = site.name;
  }

  function clearSite() {
    selectedSite = null;
    siteSearch.query = '';
  }

  function toggleRef(values: FilterRef[], ref: FilterRef): FilterRef[] {
    return values.some((value) => value.id === ref.id)
      ? values.filter((value) => value.id !== ref.id)
      : [...values, ref];
  }

  function sceneFilter(query: string): SceneFilter {
    return {
      text: query,
      site: selectedSite ? { id: selectedSite.stash_id, name: selectedSite.name } : null,
      scope,
      performers,
      performersAll,
      tags,
      tagsAll,
      year,
      duration,
      sort,
      hideOwned: false,
    };
  }

  function sceneQuery(query: string, page: number) {
    return {
      ...sceneApiQuery(sceneFilter(query), page),
      provider: selectedProvider || undefined,
      date: releaseDate || undefined,
      date_op: releaseDate ? dateOp : undefined,
    };
  }

  async function searchPages(query: string, signal: AbortSignal): Promise<PickerResults> {
    const scenes: SceneMeta[] = [];
    let total = 0;
    let perPage = 0;
    const exactScope = selectedSite !== null && releaseDate !== '' && dateOp === 'on';
    const pageLimit = exactScope ? MAX_EXACT_PAGES : 1;

    for (let pageNumber = 1; pageNumber <= pageLimit; pageNumber += 1) {
      const page = await api.adultDiscover(sceneQuery(query, pageNumber), signal);
      if (pageNumber === 1) {
        total = page.total;
        perPage = page.per_page;
      }
      scenes.push(...page.scenes);
      const complete =
        page.per_page <= 0 ||
        pageNumber * page.per_page >= page.total ||
        page.scenes.length === 0;
      if (complete) {
        return { page: 1, per_page: scenes.length, total, scenes, limited: false };
      }
    }

    return { page: 1, per_page: scenes.length, total, scenes, limited: true };
  }

  const search = createTypeahead<PickerResults>({
    initial: untrack(() => fallbackQuery),
    blank,
    minQuery: 0,
    searchBlank: true,
    depends: () => [
      selectedProvider,
      selectedSite?.stash_id,
      releaseDate,
      dateOp,
      scope,
      performers.map((ref) => ref.id).join(','),
      performersAll,
      tags.map((ref) => ref.id).join(','),
      tagsAll,
      year,
      duration,
      sort,
      session.adult,
    ],
    run: (query, signal) => session.adult ? searchPages(query, signal) : Promise.resolve(blank()),
  });

  function searchHint(): string {
    return t('component.scenePicker.filterHint', {
      provider: selectedProvider ? providerLabel(selectedProvider) : t('component.scenePicker.defaultProvider'),
      site: selectedSite?.name ?? t('component.scenePicker.anySite'),
      date: releaseDate ? formatDate(releaseDate) : t('component.scenePicker.anyDate'),
    });
  }

  function rowKey(scene: SceneMeta): string {
    return `${scene.provider}:${scene.stash_id}`;
  }

  function onSearchKeydown(event: KeyboardEvent) {
    if (event.key !== 'ArrowDown') return;
    const first = sceneList?.querySelector<HTMLElement>('button');
    if (!first) return;
    event.preventDefault();
    first.focus();
  }

  function onListKeydown(event: KeyboardEvent) {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
    const buttons = [...(sceneList?.querySelectorAll<HTMLElement>('button') ?? [])];
    const index = buttons.indexOf(event.target as HTMLElement);
    if (index === -1) return;
    event.preventDefault();
    if (event.key === 'ArrowDown') {
      buttons[Math.min(index + 1, buttons.length - 1)]?.focus();
    } else if (index === 0) {
      body?.querySelector<HTMLElement>('#scene-picker-query')?.focus();
    } else {
      buttons[index - 1]?.focus();
    }
  }

  async function select(scene: SceneMeta) {
    if (busyKey !== null) return;
    busyKey = rowKey(scene);
    try {
      await onpick(scene);
    } catch (error) {
      pushToast(errorText(error), 'danger');
    } finally {
      busyKey = null;
    }
  }
</script>

<Modal {title} {onclose} width="max-w-4xl">
  <div bind:this={body} class="flex flex-col gap-5 p-4">
    <section class="grid gap-4 rounded-md border border-border bg-surface p-4 md:grid-cols-2">
      <Field label={t('component.scenePicker.provider')} for="scene-picker-provider">
        <select
          id="scene-picker-provider"
          class={SELECT_CLASS}
          value={selectedProvider}
          onchange={(event) => changeProvider(event.currentTarget.value)}>
          {#each providerOptions as option (option.id)}
            <option value={option.id}>{option.name}</option>
          {/each}
        </select>
      </Field>

      <Field label={t('component.scenePicker.searchLabel')} for="scene-picker-query">
        <TextInput
          id="scene-picker-query"
          bind:value={search.query}
          type="search"
          autofocus
          onkeydown={onSearchKeydown}
          placeholder={t('component.scenePicker.placeholder')} />
      </Field>

      <Field
        label={t('component.scenePicker.site')}
        for="scene-picker-site"
        help={selectedSite ? t('component.scenePicker.siteSelected', { site: selectedSite.name }) : undefined}>
        <div class="relative">
          <TextInput
            id="scene-picker-site"
            bind:value={siteSearch.query}
            type="search"
            oninput={changeSiteQuery}
            placeholder={t('component.scenePicker.sitePlaceholder')} />
          {#if selectedSite}
            <Button variant="ghost" size="sm" class="absolute right-1 top-0.5" onclick={clearSite}>
              {t('component.actions.clear')}
            </Button>
          {/if}
        </div>
        {#if siteSearch.error}
          <p class="text-sm text-danger">{siteSearch.error}</p>
        {:else if siteSearch.loading}
          <p class="text-sm text-ink-muted">{t('component.typeahead.searching')}</p>
        {:else if !selectedSite && siteSearch.results.length > 0}
          <ul class="flex max-h-36 flex-col overflow-y-auto rounded-sm border border-border bg-raised p-1">
            {#each siteSearch.results as site (`${site.provider}:${site.stash_id}`)}
              <li>
                <button
                  type="button"
                  class="flex w-full items-center justify-between gap-2 rounded-sm px-2 py-1.5 text-left hover:bg-surface"
                  onclick={() => chooseSite(site)}>
                  <span class="truncate text-base text-ink">{site.name}</span>
                  {#if site.parent_name}
                    <span class="truncate text-sm text-ink-muted">{site.parent_name}</span>
                  {/if}
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      </Field>

      <div class="grid gap-3 sm:grid-cols-2">
        <Field label={t('component.scenePicker.releaseDate')} for="scene-picker-date">
          <TextInput id="scene-picker-date" bind:value={releaseDate} type="date" />
        </Field>
        {#if support.date_op}
          <Field label={t('component.scenePicker.dateComparison')} for="scene-picker-date-op">
            <select id="scene-picker-date-op" class={SELECT_CLASS} bind:value={dateOp}>
              {#each dateOptions as option (option.id)}
                <option value={option.id}>{option.name}</option>
              {/each}
            </select>
          </Field>
        {/if}
      </div>
    </section>

    {#if instancesError}
      <Banner tone="warning" message={t('component.scenePicker.providerSettingsUnavailable', { error: instancesError })} />
    {/if}

    <section class="flex flex-col gap-3">
      <div>
        <p class="micro-label">{t('component.scenePicker.refine')}</p>
        <p class="mt-1 text-sm text-ink-secondary">{searchHint()}</p>
      </div>

      <div class="grid gap-4 md:grid-cols-2">
        <Field label={t('component.scenePicker.performers')}>
          <div class="rounded-sm border border-border bg-surface p-2">
            {#key selectedProvider}
              <FilterTypeahead
                search={(query, signal) => api.adultPerformers(query, signal, selectedProvider || undefined)}
                selected={performers}
                ontoggle={(ref) => (performers = toggleRef(performers, ref))}
                placeholder={t('component.scenePicker.performerPlaceholder')}
                ariaLabel={t('component.scenePicker.performerSearchLabel')} />
            {/key}
          </div>
          {#if support.any_of && performers.length > 1}
            <select class={SELECT_CLASS} bind:value={performersAll} aria-label={t('component.scenePicker.performerMode')}>
              <option value={true}>{t('component.scenePicker.matchAll')}</option>
              <option value={false}>{t('component.scenePicker.matchAny')}</option>
            </select>
          {/if}
        </Field>

        <Field label={t('component.scenePicker.tags')}>
          <div class="rounded-sm border border-border bg-surface p-2">
            {#key selectedProvider}
              <FilterTypeahead
                search={(query, signal) => api.adultTags(query, signal, selectedProvider || undefined)}
                selected={tags}
                ontoggle={(ref) => (tags = toggleRef(tags, ref))}
                placeholder={t('component.scenePicker.tagPlaceholder')}
                ariaLabel={t('component.scenePicker.tagSearchLabel')} />
            {/key}
          </div>
          {#if support.any_of && tags.length > 1}
            <select class={SELECT_CLASS} bind:value={tagsAll} aria-label={t('component.scenePicker.tagMode')}>
              <option value={true}>{t('component.scenePicker.matchAll')}</option>
              <option value={false}>{t('component.scenePicker.matchAny')}</option>
            </select>
          {/if}
        </Field>
      </div>

      <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {#if support.year}
          <Field label={t('component.scenePicker.year')} for="scene-picker-year">
            <input
              id="scene-picker-year"
              class={SELECT_CLASS}
              type="number"
              min="1"
              inputmode="numeric"
              value={year || ''}
              oninput={(event) => (year = Math.max(0, Math.trunc(Number(event.currentTarget.value) || 0)))} />
          </Field>
        {/if}

        {#if support.duration}
          <Field label={t('component.scenePicker.duration')} for="scene-picker-duration">
            <input
              id="scene-picker-duration"
              class={SELECT_CLASS}
              type="number"
              min="1"
              inputmode="numeric"
              value={duration || ''}
              oninput={(event) => (duration = Math.max(0, Math.trunc(Number(event.currentTarget.value) || 0)))} />
          </Field>
        {/if}

        {#if support.site_scope && selectedSite}
          <Field label={t('component.scenePicker.siteScope')} for="scene-picker-scope">
            <select id="scene-picker-scope" class={SELECT_CLASS} bind:value={scope}>
              {#each SCENE_SITE_SCOPES as option (option.key)}
                <option value={option.key}>{option.label}</option>
              {/each}
            </select>
          </Field>
        {/if}

        <Field label={t('component.scenePicker.sort')} for="scene-picker-sort">
          <select id="scene-picker-sort" class={SELECT_CLASS} bind:value={sort}>
            {#each sortOptions as option (option.id)}
              <option value={option.id}>{option.name}</option>
            {/each}
          </select>
        </Field>
      </div>
    </section>

    {#if search.results.limited && !search.loading}
      <Banner tone="warning" message={t('component.scenePicker.resultLimit', { count: MAX_EXACT_PAGES })} />
    {/if}

    {#if search.error}
      <LoadError message={search.error} onretry={search.retry} />
    {:else if search.loading}
      <SearchSkeleton />
    {:else if search.results.scenes.length === 0}
      <EmptyState
        icon="search"
        title={t('component.addItem.noMatches')}
        message={t('component.scenePicker.noFilteredMatches')} />
    {:else}
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
      <ul bind:this={sceneList} class="flex flex-col gap-2" onkeydown={onListKeydown}>
        {#each search.results.scenes as scene (rowKey(scene))}
          <li class="flex items-start gap-3 rounded-md border border-border p-2 transition-colors duration-150 ease-out hover:bg-raised focus-within:bg-raised">
            <div class="w-12 shrink-0">
              <Poster path={scene.image_url} alt="" fallbackIcon="flame" />
            </div>
            <div class="min-w-0 flex-1">
              <p class="truncate text-base font-medium text-ink" title={scene.title}>{scene.title}</p>
              <div class="mt-1 flex flex-wrap items-center gap-1.5">
                <Badge tone="info">{scene.site_name}</Badge>
                <Badge mono tone="neutral">{formatDate(scene.date)}</Badge>
                <Badge tone="neutral">{providerLabel(scene.provider)}</Badge>
              </div>
              {#if scene.performers.length > 0}
                <p class="truncate text-sm text-ink-secondary" title={scene.performers.join(', ')}>
                  {scene.performers.join(', ')}
                </p>
              {:else if scene.overview}
                <p class="line-clamp-2 text-sm text-ink-secondary" title={scene.overview}>
                  {scene.overview}
                </p>
              {/if}
            </div>
            <Button
              variant="primary"
              size="sm"
              disabled={busyKey !== null}
              onclick={() => void select(scene)}>
              {busyKey === rowKey(scene) ? t('component.actions.working') : t('component.actions.match')}
            </Button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</Modal>
