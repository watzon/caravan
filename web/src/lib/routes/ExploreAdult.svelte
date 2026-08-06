<script lang="ts">
  /**
   * Explore → Adult: the provider's scene catalogue behind a filter rail
   * (PLAN phase 12 tasks 4-6). It replaces the Adult section's Scenes tab,
   * which is retired: browsing a catalogue belongs beside the other two
   * catalogues, and the library shelf is for what Caravan already holds.
   *
   * Moving it did not weaken the gate. `/discover/adult` is an adult route
   * (router.ts isAdultRoute names it), App.svelte gates the RENDER on
   * `session.adult` as well as redirecting, and every endpoint below lives on
   * the server's adult mux and answers 404 — byte-identical to an unrouted
   * path — to a caller the module is not visible to.
   *
   * The verb is Request for everybody, admins included, for the reason the
   * Scenes tab gave: approving a scene request adds the SITE, so an admin
   * "add" here would be one click for several hundred scenes.
   */
  import { ApiError, api, errorText } from '../api/client';
  import type { AdultDiscoverPage, SceneMeta } from '../api/types';
  import AppliedChips from '../components/AppliedChips.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import ExploreScopes from '../components/ExploreScopes.svelte';
  import FilterOptions from '../components/FilterOptions.svelte';
  import FilterPill from '../components/FilterPill.svelte';
  import FilterRange from '../components/FilterRange.svelte';
  import FilterTypeahead from '../components/FilterTypeahead.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import SceneCard from '../components/SceneCard.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import TextInput from '../components/TextInput.svelte';
  import Toggle from '../components/Toggle.svelte';
  import {
    clearedSceneFilter,
    parseSceneFilter,
    removeSceneChip,
    matchCountLine,
    sceneApiQuery,
    sceneChips,
    sceneFilterHref,
    sceneYearNow,
    toggleRef,
    SCENE_SITE_SCOPES,
    SCENE_SORTS,
    type FilterRef,
    type SceneFilter,
    type SceneSiteScope,
    type SortChoice,
  } from '../explore';
  import { sceneYear } from '../adult';
  import { navigate, router } from '../router.svelte';
  import { session } from '../state/session.svelte';
  import { pushToast } from '../state/toast.svelte';

  const SELECT_CLASS =
    'h-8 rounded-sm border border-border-strong bg-raised px-2 text-base text-ink ' +
    'focus:border-accent focus:outline-none';

  let filter = $derived(parseSceneFilter(router.params));
  let chips = $derived(sceneChips(filter));

  let scenes = $state<SceneMeta[]>([]);
  let answer = $state<AdultDiscoverPage | null>(null);
  let loading = $state(true);
  let loadingMore = $state(false);
  let error = $state<string | null>(null);
  let status = $state(0);
  /** The scene whose Request call is in flight. */
  let requesting = $state<string | null>(null);
  /** The search box, which commits to the URL on submit rather than per key. */
  let text = $state('');

  let inFlight: AbortController | null = null;

  let question = $derived(JSON.stringify(sceneApiQuery(filter, 1)));

  async function load(pageNumber: number) {
    inFlight?.abort();
    const controller = new AbortController();
    inFlight = controller;
    if (pageNumber <= 1) loading = true;
    else loadingMore = true;
    try {
      const fetched = await api.adultDiscover(sceneApiQuery(filter, pageNumber), controller.signal);
      answer = fetched;
      // The server echoes the page it actually served (it clamps), so that is
      // what decides append-versus-replace rather than what was asked for.
      scenes = fetched.page > 1 ? mergeScenes(scenes, fetched.scenes) : fetched.scenes;
      error = null;
      status = 0;
    } catch (err) {
      if (controller.signal.aborted) return;
      error = errorText(err);
      status = err instanceof ApiError ? err.status : 0;
    } finally {
      if (!controller.signal.aborted) {
        loading = false;
        loadingMore = false;
      }
    }
  }

  function mergeScenes(existing: SceneMeta[], fetched: SceneMeta[]): SceneMeta[] {
    const seen = new Set(existing.map((s) => s.stash_id));
    return [...existing, ...fetched.filter((s) => !seen.has(s.stash_id))];
  }

  $effect(() => {
    void question;
    scenes = [];
    answer = null;
    void load(1);
  });

  // The box follows the URL, so a Back out of a search puts the old words back
  // in it rather than leaving a field that disagrees with the results below.
  $effect(() => {
    text = filter.text;
  });

  function apply(next: SceneFilter) {
    navigate(sceneFilterHref(next), { replace: true });
  }

  function setScope(key: string) {
    apply({ ...filter, scope: key as SceneSiteScope });
  }

  async function request(scene: SceneMeta) {
    requesting = scene.stash_id;
    try {
      await api.createRequest({
        media_type: 'scene',
        // A scene is named by its stash-box id and nothing else; the server
        // refuses a scene request that also carries a tmdb id.
        tmdb_id: 0,
        stash_id: scene.stash_id,
        title: scene.title,
        year: sceneYear(scene.date),
        poster_path: scene.image_url,
      });
      // Patch in place rather than refetch: one flag on one card changed, and a
      // refetch is another round trip to the provider.
      scenes = scenes.map((s) =>
        s.stash_id === scene.stash_id ? { ...s, requested: true } : s,
      );
      pushToast(`Requested ${scene.title}`, 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      requesting = null;
    }
  }

  let visible = $derived(filter.hideOwned ? scenes.filter((s) => !s.in_library) : scenes);
  /**
   * "18,442 scenes match" — the provider gives a real total here, unlike the
   * title scopes, whose page carries only a page count. A number nobody can
   * trust is worse than none, so this line exists on the one scope that has one.
   */
  let countNote = $derived(error ? '' : matchCountLine(answer?.total ?? 0, 'scene'));
  let hasMore = $derived(
    answer !== null &&
      answer.per_page > 0 &&
      scenes.length < answer.total &&
      scenes.length >= answer.per_page,
  );
  let nextPage = $derived((answer?.page ?? 0) + 1);
  /**
   * Which nothing this is. "Hide in library" can empty a page the provider
   * filled, and blaming the provider for that would send somebody to check a
   * setting that is working — so the toggle owns up to it, and Load more stays
   * on screen because there is still a next page to reach.
   */
  let emptyMessage = $derived(
    filter.hideOwned && scenes.length > 0
      ? 'Every scene on this page is already in the library. Turn off "Hide in library" to see them.'
      : chips.length === 0
        ? 'The metadata provider returned nothing.'
        : 'No scene matches every filter. Try removing one.',
  );
  /**
   * What the configured endpoint can actually answer (PLAN phase 12,
   * acceptance criterion 1: "nothing renders a control the provider cannot
   * answer"). "stash-box" is a protocol with dialects — TPDB serves a release
   * year, a runtime, a widened site scope and two extra orderings, and a
   * StashDB or FansDB install refuses all of them — so which pills exist is a
   * property of the server, not of this file.
   *
   * The answer comes from /auth/me rather than from the scene answer, because
   * it has to be readable BEFORE the first request: a URL naming an
   * unsupported filter 400s, and a 400 carries no capabilities to learn from.
   */
  let can = $derived(session.sceneFilters);

  /**
   * Both any/all switches exist, but only once there are two things to
   * combine — and only where "any" is a question the endpoint can be asked. A
   * dialect that can only say "carries all of these" gets no toggle rather
   * than a toggle whose other half is a 400; the chip row still says `all`, so
   * what is being asked is never a guess.
   */
  let tagModeVisible = $derived(filter.tags.length > 1 && can.any_of);
  let performerModeVisible = $derived(filter.performers.length > 1 && can.any_of);

  /** The orderings this endpoint offers, in the rail's order. */
  let sortOptions = $derived(
    SCENE_SORTS.filter(
      (option) =>
        (option.key !== 'duration' || can.sort_duration) &&
        (option.key !== 'relevance' || can.sort_relevance),
    ),
  );

  /**
   * What Clear filters leaves behind. `clearedSceneFilter` keeps the sort on
   * purpose — it is not a chip, so clearing it would remove something the
   * button does not appear to be about — but that leaves one dead end: a link
   * carrying `sort=duration` opened against an endpoint with no duration
   * ordering 400s, and a Clear that kept the sort would 400 again. So the sort
   * survives EXCEPT when the sort is the thing that cannot be served.
   */
  let cleared = $derived.by(() => {
    const base = clearedSceneFilter(filter);
    const first = sortOptions[0] as SortChoice;
    return sortOptions.some((option) => option.key === base.sort)
      ? base
      : { ...base, sort: first.key };
  });
</script>

<div class="flex flex-col gap-6">
  <ExploreScopes active="adult" note={countNote} />

  <div class="flex flex-wrap items-center gap-2">
    <form
      class="flex w-full min-w-0 items-center gap-2 sm:w-auto"
      onsubmit={(event) => {
        event.preventDefault();
        apply({ ...filter, text: text.trim() });
      }}>
      <div class="min-w-0 flex-1 sm:w-56 sm:flex-none">
        <TextInput
          bind:value={text}
          type="search"
          placeholder="Search scenes…"
          ariaLabel="Search the metadata provider for scenes" />
      </div>
      <Button class="shrink-0" variant="secondary" type="submit" disabled={loading}>
        <Icon name="search" size={14} />
        Search
      </Button>
    </form>

    <FilterPill label="Site" applied={filter.site !== null} width="w-72">
      {#snippet children()}
        <div class="flex flex-col gap-3">
          <FilterTypeahead
            search={async (q, signal) =>
              (await api.searchSites(q, signal)).map((site) => ({
                id: site.stash_id,
                name: site.name,
                hint: site.parent_name,
              }))}
            selected={filter.site ? [filter.site] : []}
            ontoggle={(ref: FilterRef) =>
              apply(
                filter.site?.id === ref.id
                  ? { ...filter, site: null, scope: 'site' }
                  : { ...filter, site: ref },
              )}
            placeholder="Search sites…"
            ariaLabel="Search sites" />

          {#if filter.site && can.site_scope}
            <!-- The widening ladder only exists once there is something to
                 widen: the API answers 400 to a scope with no site, and a
                 control that is a guaranteed error is not a control. The same
                 reasoning drops it on an endpoint with no widening operator at
                 all, which is every stash-box but TPDB's. -->
            <div class="flex flex-col gap-1 border-t border-border pt-2">
              <FilterOptions
                options={SCENE_SITE_SCOPES.map((s) => ({ id: s.key, name: s.label, hint: s.hint }))}
                selected={[filter.scope]}
                onselect={setScope} />
            </div>
          {/if}
        </div>
      {/snippet}
    </FilterPill>

    <FilterPill label="Performers" applied={filter.performers.length > 0} width="w-72">
      {#snippet children()}
        <FilterTypeahead
          search={async (q, signal) =>
            (await api.adultPerformers(q, signal)).map((p) => ({ id: p.id, name: p.name }))}
          selected={filter.performers}
          ontoggle={(ref: FilterRef) =>
            apply({ ...filter, performers: toggleRef(filter.performers, ref) })}
          placeholder="Search performers…"
          ariaLabel="Search performers" />
      {/snippet}
    </FilterPill>

    <FilterPill label="Tags" applied={filter.tags.length > 0} width="w-72">
      {#snippet children()}
        <FilterTypeahead
          search={async (q, signal) =>
            (await api.adultTags(q, signal)).map((t) => ({ id: t.id, name: t.name }))}
          selected={filter.tags}
          ontoggle={(ref: FilterRef) => apply({ ...filter, tags: toggleRef(filter.tags, ref) })}
          placeholder="Search tags…"
          ariaLabel="Search tags" />
      {/snippet}
    </FilterPill>

    <!-- Year and Duration are TPDB's; the generic stash-box scene query has no
         field for either and refuses both. Absent rather than disabled: a
         greyed pill invites somebody to go looking for the setting that would
         un-grey it, and there is none. -->
    {#if can.year}
      <FilterPill label="Year" applied={filter.year > 0}>
        {#snippet children()}
          <FilterRange
            minValue={filter.year}
            minLabel="Released in"
            onmin={(value) => apply({ ...filter, year: value })}
            placeholder={String(sceneYearNow())} />
        {/snippet}
      </FilterPill>
    {/if}

    {#if can.duration}
      <FilterPill label="Duration" applied={filter.duration > 0}>
        {#snippet children()}
          <FilterRange
            minValue={filter.duration}
            minLabel="Minutes"
            onmin={(value) => apply({ ...filter, duration: value })}
            placeholder="30"
            hint="The provider takes one duration with no comparison, so this is a single value rather than a range." />
        {/snippet}
      </FilterPill>
    {/if}

    <div class="ml-auto flex items-center gap-3">
      <Toggle
        checked={filter.hideOwned}
        label="Hide in library"
        onchange={(next) => apply({ ...filter, hideOwned: next })} />
      <select
        value={filter.sort}
        aria-label="Sort results"
        onchange={(event) => apply({ ...filter, sort: event.currentTarget.value })}
        class={SELECT_CLASS}>
        {#each sortOptions as option (option.key)}
          <option value={option.key}>{option.label}</option>
        {/each}
      </select>
    </div>
  </div>

  <AppliedChips
    {chips}
    onremove={(key) => apply(removeSceneChip(filter, key))}
    onclear={() => apply(cleared)}>
    {#snippet trailing()}
      {#if performerModeVisible}
        <button
          type="button"
          aria-label={`Match performers: ${filter.performersAll ? 'all' : 'any'}`}
          aria-pressed={filter.performersAll}
          onclick={() => apply({ ...filter, performersAll: !filter.performersAll })}
          class="inline-flex h-7 items-center rounded-full border border-border bg-surface px-3
                 font-mono text-xs text-ink-secondary transition-colors duration-150 ease-out
                 hover:border-border-strong hover:text-ink">
          performers: {filter.performersAll ? 'all' : 'any'}
        </button>
      {/if}
      {#if tagModeVisible}
        <button
          type="button"
          aria-label={`Match tags: ${filter.tagsAll ? 'all' : 'any'}`}
          aria-pressed={filter.tagsAll}
          onclick={() => apply({ ...filter, tagsAll: !filter.tagsAll })}
          class="inline-flex h-7 items-center rounded-full border border-border bg-surface px-3
                 font-mono text-xs text-ink-secondary transition-colors duration-150 ease-out
                 hover:border-border-strong hover:text-ink">
          tags: {filter.tagsAll ? 'all' : 'any'}
        </button>
      {/if}
    {/snippet}
  </AppliedChips>

  <!-- 503 is "no stash-box credential" — a setup problem with a destination,
       not something a retry fixes. 400 is the other one worth its own words:
       the configured endpoint cannot express one of these filters, and the
       server named which. Neither is a reason to offer Retry. -->
  {#if error && status === 503}
    <EmptyState
      icon="settings"
      title="No metadata source configured"
      message="Scenes come from a stash-box endpoint, so this needs an API key. Add one under Settings → Adult content and this screen fills in.">
      {#snippet action()}
        <Button variant="primary" href="/settings/adult">Open adult settings</Button>
      {/snippet}
    </EmptyState>
  {:else if error && status === 400}
    <EmptyState icon="warning" title="This endpoint cannot answer that" message={error}>
      {#snippet action()}
        <Button variant="secondary" onclick={() => apply(cleared)}>
          Clear filters
        </Button>
      {/snippet}
    </EmptyState>
  {:else if error}
    <LoadError message={error} onretry={() => void load(1)} />
  {:else if loading && scenes.length === 0}
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {#each Array.from({ length: 6 }) as _, i (i)}
        <Skeleton class="aspect-video w-full rounded-md" />
      {/each}
    </div>
  {:else if visible.length === 0}
    <!-- Three different nothings, and saying the wrong one sends somebody
         looking in the wrong place. "Hide in library" swallowing a full page
         is the one that reads as a provider failure but is not: the grid is
         empty because of a toggle on this screen, and Load more below still
         reaches the next page. -->
    <EmptyState
      icon="flame"
      title="No scenes match"
      message={emptyMessage} />
  {:else}
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {#each visible as scene (scene.stash_id)}
        <SceneCard
          {scene}
          requesting={requesting === scene.stash_id}
          busy={requesting !== null}
          onrequest={request} />
      {/each}
    </div>
  {/if}

  <!-- Outside the if/else chain, as it is on the title scopes: a page the
       hide-in-library toggle emptied still has a page after it, and burying
       this in the results branch made that a dead end. -->
  {#if hasMore && !error}
    <div class="flex justify-center">
      <Button variant="secondary" disabled={loadingMore} onclick={() => void load(nextPage)}>
        {loadingMore ? 'Loading…' : 'Load more'}
      </Button>
    </div>
  {/if}
</div>
