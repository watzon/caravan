<script lang="ts">
  /**
   * The universal indexer search (plan part B8): any query, any categories,
   * every enabled indexer — Prowlarr-style.
   *
   * It exists for what the per-item pickers cannot reach. Their queries are
   * derived from a library item, so they can only ever find releases for
   * things Caravan already tracks, under names its builders think to try. This
   * screen has neither limit: it asks exactly what the user typed, and what it
   * finds does not have to be a library item at all.
   *
   * Nothing here is grabbed on one click. A result has no item behind it, so
   * "where does this land" is a real question, and GrabTargetModal asks it.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Indexer, IndexerError, Release } from '../api/types';
  import Banner from '../components/Banner.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import GrabTargetModal from '../components/GrabTargetModal.svelte';
  import IndexerErrors from '../components/IndexerErrors.svelte';
  import LoadError from '../components/LoadError.svelte';
  import ReleaseSearchControls from '../components/ReleaseSearchControls.svelte';
  import ReleaseTable from '../components/ReleaseTable.svelte';
  import { navigate, router } from '../router.svelte';
  import { parseReleaseSearch, releaseSearchHref } from '../search';

  let query = $state('');
  let categories = $state<number[]>([]);
  let indexerIDs = $state<number[]>([]);
  let indexers = $state<Indexer[]>([]);

  /** Null until the first search: "nothing yet" and "found nothing" differ. */
  let releases = $state<Release[] | null>(null);
  let failures = $state<IndexerError[]>([]);
  let truncated = $state(false);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let askedCount = $state(0);
  /** The release whose target dialog is open. */
  let grabbing = $state<Release | null>(null);

  onMount(() => {
    // The URL is the search. Seeding from it — rather than only writing to it
    // — is what makes a shared link reproduce the results it was shared for.
    const seeded = parseReleaseSearch(router.params);
    query = seeded.q;
    categories = seeded.cats;
    indexerIDs = seeded.indexers;

    void api
      .listIndexers()
      .then((list) => (indexers = list))
      // The rail simply offers no indexer filter when the list cannot be
      // fetched; the search still fans out over every enabled one.
      .catch(() => (indexers = []));

    if (seeded.q !== '') void run();
  });

  async function search() {
    // Written before the request, so the address bar is honest about what is
    // in flight and a reload during a slow fan-out repeats the same search.
    navigate(releaseSearchHref({ q: query, cats: categories, indexers: indexerIDs }), {
      replace: true,
    });
    await run();
  }

  async function run() {
    const asked = query.trim();
    if (asked === '') return;
    loading = true;
    releases = null;
    try {
      const found = await api.searchReleases({
        q: asked,
        cats: categories,
        indexer_ids: indexerIDs,
      });
      releases = found.releases;
      failures = found.errors ?? [];
      truncated = found.truncated === true;
      askedCount = countAsked();
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  /**
   * How many indexers this search reached, snapshotted with the answer: the
   * rail stays editable afterwards, and "2 of 5 answered" must keep describing
   * the results on screen rather than the boxes currently ticked.
   */
  function countAsked(): number {
    const enabled = indexers.filter((indexer) => indexer.enabled);
    if (indexerIDs.length === 0) return enabled.length;
    return enabled.filter((indexer) => indexerIDs.includes(indexer.id)).length;
  }

  function grabbed() {
    // The queue is where the answer to "did that work" lives.
    navigate('/queue');
  }
</script>

<div class="flex flex-col gap-6">
  <div class="min-w-0">
    <h2 class="font-display text-xl font-semibold tracking-tight text-ink">Search indexers</h2>
    <p class="text-base text-ink-secondary">
      Every enabled indexer, asked exactly what you type. A result does not have to be something
      Caravan already tracks.
    </p>
  </div>

  <ReleaseSearchControls
    bind:query
    bind:categories
    bind:indexerIDs
    {indexers}
    busy={loading}
    onsearch={search} />

  <IndexerErrors errors={failures} total={askedCount || undefined} />

  {#if truncated && releases}
    <!-- The cut is on the ANSWER, not the cache: every row was cached before
         it, so a narrower re-search can still grab what fell off the end. -->
    <Banner
      tone="info"
      icon="warning"
      title="Showing the highest-scoring results only"
      message="That query matched more than this screen shows. Narrow it, or filter by category, to see the rest." />
  {/if}

  {#if error}
    <LoadError message={error} onretry={run} />
  {:else if releases === null && !loading}
    <EmptyState
      icon="search"
      title="Search every enabled indexer"
      message="Nothing is grabbed until you say so — and when you do, you choose where it lands." />
  {:else}
    <ReleaseTable
      {releases}
      {loading}
      busyGUID={null}
      grabLabel="Grab into…"
      emptyMessage="No enabled indexer returned anything for that query. Try a release name, drop the year, or widen the categories."
      ongrab={(release) => (grabbing = release)} />
  {/if}
</div>

{#if grabbing}
  <GrabTargetModal
    release={grabbing}
    ongrabbed={grabbed}
    onclose={() => (grabbing = null)} />
{/if}
