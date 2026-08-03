<script lang="ts">
  /**
   * The one dialog that puts a discovered title into the pipeline, in two
   * modes.
   *
   * - `request` records a wish (POST /requests). No profile, no folder, no
   *   search switch: none of those are the requester's to decide, and offering
   *   them would imply an approval that has not happened. A movie's minimum
   *   availability IS the requester's — "when do I want this" is part of the
   *   ask — so that one field lives in both modes.
   * - `add` puts it in the library now, through the same endpoints the ⌘K add
   *   flow uses. Approving a pending request is this mode with a `requestID`,
   *   which routes the add through POST /requests/{id}/approve so the row is
   *   marked approved by the add rather than by a second write.
   *
   * Season selection is real in both modes, and in both it is the `seasons`
   * field of the one call: the request stores it, the add applies it as
   * monitoring. An unchecked season is unmonitored, which is exactly "do not
   * go get this one", and it also stops the add from closing a pending request
   * for a season nobody went after (internal/api/library.go).
   *
   * The season maths is in lib/discover.ts and unit-tested there; this file is
   * wiring and I/O.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type {
    DiscoverSeason,
    MediaType,
    MinAvailability,
    Movie,
    QualityProfile,
    Series,
  } from '../api/types';
  import {
    AVAILABILITY_OPTIONS,
    absorbNote,
    addSeasons,
    allSeasonNumbers,
    allSelected,
    availabilityHint,
    defaultSeasonSelection,
    modalSubtitle,
    requestSeasons,
    seasonMeta,
    selectableSeasons,
    submitLabel,
    toggleSeason,
    type AddRequestResult,
    type RequestMode,
  } from '../discover';
  import { formatBytes, seasonLabel } from '../format';
  import { navigate } from '../router.svelte';
  import { readSearchOnAdd, writeSearchOnAdd } from '../searchOnAdd';
  import { pushToast } from '../state/toast.svelte';
  import { system } from '../state/system.svelte';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import Modal from './Modal.svelte';
  import Skeleton from './Skeleton.svelte';

  interface Props {
    mode: RequestMode;
    mediaType: MediaType;
    tmdbID: number;
    title: string;
    year: number;
    /** Provider poster path; round-trips into the request row unchanged. */
    posterPath?: string;
    /** Season metadata. Omitted for a series makes the modal fetch it. */
    seasons?: DiscoverSeason[] | null;
    /** Season numbers checked on open (a per-season Request, an approval). */
    preselect?: number[] | null;
    /** Approving this pending request rather than adding from scratch. */
    requestID?: number | null;
    /**
     * The availability stage checked on open — an approval passes the
     * request's stored choice so the approver sees what was asked for.
     */
    initialAvailability?: MinAvailability | '' | null;
    onclose: () => void;
    ondone?: (result: AddRequestResult) => void;
  }

  let {
    mode,
    mediaType,
    tmdbID,
    title,
    year,
    posterPath = '',
    seasons = null,
    preselect = null,
    requestID = null,
    initialAvailability = null,
    onclose,
    ondone,
  }: Props = $props();

  const SELECT_CLASS =
    'h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink ' +
    'focus:border-accent focus:outline-none disabled:opacity-50';

  let seasonList = $state<DiscoverSeason[]>([]);
  let selected = $state<number[]>([]);
  let loadingSeasons = $state(false);
  let profiles = $state<QualityProfile[]>([]);
  /** 0 means "whatever the library defaults to" — the add endpoints' behaviour. */
  let profileID = $state(0);
  let searchNow = $state(readSearchOnAdd());
  /**
   * Movies only: the release stage the automatic search waits for. Requesters
   * choose it too — it is part of the ask, not part of the approval — and the
   * approve endpoint accepts it as an override, so the field is live in every
   * mode the modal has.
   */
  let minAvailability = $state<MinAvailability>(initialAvailability || 'released');
  let busy = $state(false);

  /**
   * Approving is an add without a profile: POST /requests/{id}/approve takes a
   * search flag and nothing else, so the select would be a control with no
   * effect. It is hidden rather than disabled.
   */
  let canChooseProfile = $derived(mode === 'add' && requestID === null);

  let selectable = $derived(selectableSeasons(seasonList));
  let note = $derived(absorbNote(seasonList, selected, mode));
  let everySelected = $derived(allSelected(seasonList, selected));
  let subtitle = $derived(modalSubtitle(mediaType, title, year, seasonList));
  let primaryLabel = $derived(submitLabel(mode, mediaType, selected.length));
  let canSubmit = $derived(
    !busy && !loadingSeasons && (mediaType === 'movie' || selectable.length === 0 || selected.length > 0),
  );

  function seed(list: DiscoverSeason[]) {
    seasonList = list;
    selected =
      preselect && preselect.length > 0
        ? [...preselect].sort((a, b) => a - b)
        : defaultSeasonSelection(list, mode);
  }

  onMount(() => {
    if (mediaType === 'series' && seasons === null) {
      loadingSeasons = true;
      void api
        .discoverTitle('series', tmdbID)
        .then((detail) => seed(detail.seasons ?? []))
        .catch((err) => {
          // Without season data the modal still works as a whole-title ask,
          // which is better than refusing to open. Say so rather than hide it.
          pushToast(errorText(err), 'warning');
          seed([]);
        })
        .finally(() => (loadingSeasons = false));
    } else {
      seed(mediaType === 'series' ? (seasons ?? []) : []);
    }

    if (canChooseProfile) {
      void api.listQualityProfiles().then((rows) => (profiles = rows)).catch(() => {
        // A missing profile list is not a reason to block the add: 0 keeps the
        // server's default.
        profiles = [];
      });
    }
  });

  function setSearchNow(next: boolean) {
    searchNow = next;
    writeSearchOnAdd(next);
  }

  async function submit() {
    if (!canSubmit) return;
    busy = true;
    try {
      if (mode === 'request') {
        await sendRequest();
      } else {
        await addToLibrary();
      }
    } catch (err) {
      // 409 on either path means the view is stale (already in the library, or
      // the request is no longer pending). The server's own words say which.
      pushToast(errorText(err), 'danger');
    } finally {
      busy = false;
    }
  }

  async function sendRequest() {
    const created = await api.createRequest({
      media_type: mediaType,
      tmdb_id: tmdbID,
      title,
      year,
      poster_path: posterPath,
      ...(mediaType === 'series'
        ? { seasons: requestSeasons(seasonList, selected) }
        : { min_availability: minAvailability }),
    });
    pushToast(`Requested ${title}`, 'success');
    ondone?.({ kind: 'requested', request: created });
    onclose();
  }

  async function addToLibrary() {
    const added = requestID === null ? await addDirect() : await approve();
    // Monitoring came with the add itself; the search is queued after it,
    // never as part of it, so it cannot go after an unchecked season.
    if (mediaType === 'series' && searchNow) await api.searchSeriesNow(added.id);
    if (canChooseProfile && profileID > 0) await applyProfile(added.id);

    pushToast(`Added ${added.title}`, 'success');
    ondone?.({ kind: 'added', mediaType, libraryID: added.id });
    onclose();
    navigate(mediaType === 'movie' ? `/movies/${added.id}` : `/series/${added.id}`);
  }

  /**
   * A series is searched after the add, never as part of it: search_missing at
   * add time would go after every season, including the ones the user just
   * unchecked.
   */
  function addDirect(): Promise<Movie | Series> {
    return mediaType === 'movie'
      ? api.addMovie({
          tmdb_id: tmdbID,
          monitored: true,
          search_now: searchNow,
          min_availability: minAvailability,
        })
      : api.addSeries({
          tmdb_id: tmdbID,
          monitored: true,
          search_missing: false,
          seasons: addSeasons(seasonList, selected),
        });
  }

  async function approve(): Promise<Movie | Series> {
    const result = await api.approveRequest(
      requestID as number,
      mediaType === 'movie' && searchNow,
      mediaType === 'series' ? addSeasons(seasonList, selected) : undefined,
      mediaType === 'movie' ? minAvailability : undefined,
    );
    const added = mediaType === 'movie' ? result.movie : result.series;
    if (!added) throw new Error('the approval did not return the added title');
    return added;
  }

  async function applyProfile(id: number) {
    if (mediaType === 'movie') {
      await api.setMovieQualityProfile(id, profileID);
    } else {
      await api.setSeriesQualityProfile(id, profileID);
    }
  }
</script>

<Modal
  title={mode === 'add' ? 'Add to library' : 'Request'}
  width="max-w-xl"
  {onclose}>
  <div class="flex flex-col gap-4 p-4">
    <p class="text-base text-ink-secondary">{subtitle}</p>

    {#if mediaType === 'series'}
      {#if loadingSeasons}
        <div class="flex flex-col gap-2">
          {#each Array.from({ length: 3 }) as _, i (i)}
            <Skeleton class="h-10 w-full rounded-sm" />
          {/each}
        </div>
      {:else if seasonList.length > 0}
        <div class="flex flex-col gap-2">
          <div class="flex items-baseline gap-3">
            <span class="micro-label">Seasons</span>
            {#if selectable.length > 0}
              <button
                type="button"
                class="ml-auto text-sm text-accent-text transition-colors duration-150 ease-out hover:text-accent"
                onclick={() => (selected = everySelected ? [] : allSeasonNumbers(seasonList))}>
                {everySelected ? 'Deselect all' : 'Select all'}
              </button>
            {/if}
          </div>

          <ul class="flex flex-col divide-y divide-border overflow-hidden rounded-md border border-border">
            {#each seasonList as season (season.season_number)}
              {@const meta = seasonMeta(season)}
              {@const checked = selected.includes(season.season_number)}
              <li>
                <label
                  class="flex h-12 cursor-pointer items-center gap-3 px-3 transition-colors duration-150 ease-out hover:bg-raised
                         {season.in_library ? 'cursor-default opacity-60' : ''}">
                  <input
                    type="checkbox"
                    class="size-4 accent-accent"
                    disabled={season.in_library}
                    {checked}
                    onchange={() => (selected = toggleSeason(selected, season.season_number))} />
                  <span class="text-base text-ink">{seasonLabel(season.season_number)}</span>
                  {#if meta}
                    <span class="font-mono text-xs text-ink-muted">{meta}</span>
                  {/if}
                  <span class="ml-auto">
                    {#if season.in_library}
                      <Badge tone="success">In library</Badge>
                    {:else if season.requested}
                      <Badge tone="warning">Requested</Badge>
                    {/if}
                  </span>
                </label>
              </li>
            {/each}
          </ul>

          {#if note}
            <p class="text-sm text-warning">{note}</p>
          {/if}
        </div>
      {/if}
    {/if}

    {#if mediaType === 'movie'}
      <div class="flex flex-col gap-1">
        <Field label="Minimum availability" for="add-availability">
          <select id="add-availability" bind:value={minAvailability} class={SELECT_CLASS}>
            {#each AVAILABILITY_OPTIONS as option (option.value)}
              <option value={option.value}>{option.label}</option>
            {/each}
          </select>
        </Field>
        <p class="text-sm text-ink-muted">{availabilityHint(minAvailability)}</p>
      </div>
    {/if}

    {#if mode === 'add'}
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
        {#if canChooseProfile}
          <Field label="Quality profile" for="add-profile">
            <select id="add-profile" bind:value={profileID} class={SELECT_CLASS}>
              <option value={0}>Library default</option>
              {#each profiles as profile (profile.id)}
                <option value={profile.id}>{profile.name}</option>
              {/each}
            </select>
          </Field>
        {/if}

        <!-- Caravan has one storage root, so the folder is shown rather than
             chosen: a picker with a single option is a lie about the model. -->
        <Field label="Root folder" for="add-root">
          <select id="add-root" class={SELECT_CLASS} disabled>
            <option>
              {system.status?.storage_root || 'no storage root'}
              {system.status && system.status.disk_total_bytes > 0
                ? ` · ${formatBytes(system.status.disk_free_bytes)} free`
                : ''}
            </option>
          </select>
        </Field>
      </div>

      <label class="flex items-center gap-3 rounded-md border border-border bg-raised px-3 py-2">
        <input
          type="checkbox"
          checked={searchNow}
          onchange={(event) => setSearchNow(event.currentTarget.checked)}
          class="size-4 accent-accent" />
        <span class="text-base text-ink">
          {mediaType === 'series'
            ? 'Search for selected seasons right away'
            : 'Search for it right away'}
        </span>
      </label>
    {/if}
  </div>

  {#snippet footer()}
    <Button variant="secondary" disabled={busy} onclick={onclose}>Cancel</Button>
    <Button variant="primary" disabled={!canSubmit} onclick={submit}>
      {busy ? 'Working…' : primaryLabel}
    </Button>
  {/snippet}
</Modal>
