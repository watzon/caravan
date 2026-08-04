<script lang="ts">
  /**
   * Library → Adult → Scenes: discover, extended to scenes (PLAN phase 9 task
   * 7d).
   *
   * It is a route of its own rather than a shelf inside /discover, mirroring
   * the server's own arrangement and for the same reason: /discover is
   * TMDB-shaped down to its int64 ids and its network/studio shelves, and
   * merging scenes into it would put the "may this caller see scenes" decision
   * inside a screen instead of at the door. A caller the module is not visible
   * to does not get a filtered Discover — they get the Discover they had before
   * the module existed, and this screen does not exist for them at all.
   *
   * The verb is Request for everybody, admins included. A scene cannot be added
   * on its own — its number is its sequence within its site's release year, so
   * the whole catalogue is what numbers it — and approving a scene request adds
   * the SITE. Offering an admin a one-click "add" here would therefore be a
   * button whose real effect is several hundred scenes, which is a decision, not
   * a click. Adding a site outright is the Sites tab's Add button.
   */
  import { onMount } from 'svelte';
  import { ApiError, api, errorText } from '../api/client';
  import type { SceneMeta } from '../api/types';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import PageTabs from '../components/PageTabs.svelte';
  import Poster from '../components/Poster.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import TextInput from '../components/TextInput.svelte';
  import {
    ADULT_TABS,
    adultTabHref,
    sceneMetaLine,
    sceneYear,
  } from '../adult';
  import { navigate } from '../router.svelte';
  import { pushToast } from '../state/toast.svelte';

  let query = $state('');
  let scenes = $state<SceneMeta[] | null>(null);
  let page = $state(1);
  let total = $state(0);
  let perPage = $state(0);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let status = $state(0);
  /** The scene whose Request call is in flight. */
  let requesting = $state<string | null>(null);

  /**
   * A blank query is legal and is what the screen opens on: the provider
   * answers its newest scenes, sorted by release date, which is the adult
   * equivalent of a trending shelf.
   */
  async function load(nextPage = 1) {
    loading = true;
    try {
      const result = await api.adultDiscover(query.trim(), nextPage);
      // Paging appends, a new search replaces: the page number is the only
      // thing that distinguishes the two, and the server echoes back the page
      // it actually served (it clamps), so that is what is stored.
      scenes = result.page > 1 ? [...(scenes ?? []), ...result.scenes] : result.scenes;
      page = result.page;
      perPage = result.per_page;
      total = result.total;
      error = null;
      status = 0;
    } catch (err) {
      error = errorText(err);
      status = err instanceof ApiError ? err.status : 0;
    } finally {
      loading = false;
    }
  }

  onMount(() => void load());

  let hasMore = $derived(
    scenes !== null && perPage > 0 && scenes.length < total && scenes.length >= perPage,
  );

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
      // Patch in place rather than refetch: the answer changed one flag on one
      // card, and a refetch is another round trip to the provider.
      scenes = (scenes ?? []).map((s) =>
        s.stash_id === scene.stash_id ? { ...s, requested: true } : s,
      );
      pushToast(`Requested ${scene.title}`, 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      requesting = null;
    }
  }
</script>

<div class="flex flex-col gap-6">
  <PageTabs
    tabs={ADULT_TABS}
    active="scenes"
    ariaLabel="Adult sections"
    onchange={(key) => navigate(adultTabHref(key))} />

  <form
    class="flex items-center gap-2"
    onsubmit={(event) => {
      event.preventDefault();
      void load();
    }}>
    <div class="w-72">
      <TextInput
        bind:value={query}
        type="search"
        placeholder="Search scenes, performers, sites…"
        ariaLabel="Search the metadata provider for scenes" />
    </div>
    <Button variant="secondary" type="submit" disabled={loading}>
      <Icon name="search" size={14} />
      {loading ? 'Searching…' : 'Search'}
    </Button>
  </form>

  <!-- 503 is "no stash-box credential" — a setup problem with a destination,
       not something a retry fixes. It reads like DiscoverError's TMDB case, but
       it is written out here rather than folded into that component: the two
       point at different settings panes and name different providers, and one
       component that branches on which is harder to read than two that do not. -->
  {#if error && status === 503}
    <EmptyState
      icon="settings"
      title="No metadata source configured"
      message="Scenes come from a stash-box endpoint, so this needs an API key. Add one under Settings → Adult content and this screen fills in.">
      {#snippet action()}
        <Button variant="primary" href="/settings/adult">Open adult settings</Button>
      {/snippet}
    </EmptyState>
  {:else if error}
    <LoadError message={error} onretry={() => void load()} />
  {:else if loading && scenes === null}
    <div class="flex flex-col gap-2">
      {#each Array.from({ length: 4 }) as _, i (i)}
        <Skeleton class="h-20 w-full rounded-md" />
      {/each}
    </div>
  {:else if scenes !== null && scenes.length === 0}
    <EmptyState
      icon="flame"
      title="No scenes match"
      message="The metadata provider returned nothing for that search. Try a performer or a site name." />
  {:else if scenes}
    <ul class="flex flex-col gap-2">
      {#each scenes as scene (scene.stash_id)}
        <li class="flex items-center gap-3 rounded-md border border-border bg-surface p-3">
          <div class="w-16 shrink-0">
            <Poster path={scene.image_url} alt="" fallbackIcon="flame" />
          </div>

          <div class="min-w-0 flex-1">
            <p class="truncate text-base font-medium text-ink" title={scene.title}>
              {scene.title}
            </p>
            <p class="truncate text-sm text-ink-secondary">{sceneMetaLine(scene)}</p>
            {#if scene.performers.length > 0}
              <p class="truncate text-sm text-ink-muted">{scene.performers.join(', ')}</p>
            {/if}
          </div>

          <div class="flex shrink-0 items-center gap-2">
            <!-- Owned beats requested: once the library holds the scene the
                 request is moot, exactly as on a discover title card. -->
            {#if scene.in_library}
              <Badge tone="success">
                <span class="inline-flex items-center gap-1">
                  <Icon name="check" size={10} />In library
                </span>
              </Badge>
            {:else if scene.requested}
              <Badge tone="warning">
                <span class="inline-flex items-center gap-1">
                  <Icon name="clock" size={10} />Requested
                </span>
              </Badge>
            {:else}
              <Button
                variant="primary"
                size="sm"
                disabled={requesting !== null}
                onclick={() => void request(scene)}>
                <Icon name="plus" size={14} />
                {requesting === scene.stash_id ? 'Requesting…' : 'Request'}
              </Button>
            {/if}
          </div>
        </li>
      {/each}
    </ul>

    {#if hasMore}
      <Button
        variant="secondary"
        class="self-center"
        disabled={loading}
        onclick={() => void load(page + 1)}>
        {loading ? 'Loading…' : 'Load more'}
      </Button>
    {/if}
  {/if}
</div>
