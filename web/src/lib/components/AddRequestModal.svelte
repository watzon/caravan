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
  import { untrack } from 'svelte';
  import { api, errorText } from '../api/client';
  import { metadataToast } from '../credentials';
  import type {
    CreateRequestBody,
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
  import { useI18n } from '../i18n.svelte';
  import { formatBytes, seasonLabel } from '../format';
  import { navigate } from '../router.svelte';
  import { readSearchOnAdd, writeSearchOnAdd } from '../searchOnAdd';
  import { session } from '../state/session.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { system } from '../state/system.svelte';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import LoadError from './LoadError.svelte';
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
    /** Monitoring state to restore when reopening an existing request. */
    initialMonitored?: boolean | null;
    /**
     * The availability stage checked on open - an approval passes the
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
    initialMonitored = null,
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
  let profiles = $state<QualityProfile[] | null>(null);
  let loadingProfiles = $state(false);
  let profilesError = $state<string | null>(null);
  /** 0 uses the library default. */
  let profileID = $state(0);
  let searchNow = $state(untrack(readSearchOnAdd));
  /** Whether a direct add should continue searching for missing releases. */
  let monitored = $state(false);
  /**
   * Movies only: the release stage the automatic search waits for. Requesters
   * choose it too - it is part of the ask, not part of the approval - and the
   * approve endpoint accepts it as an override, so the field is live in every
   * mode the modal has.
   */
  let minAvailability = $state<MinAvailability>(untrack(() => initialAvailability || 'released'));
  let busy = $state(false);
  let sessionRevision = 0;

  const { t } = useI18n();

  let selectable = $derived(selectableSeasons(seasonList));
  let note = $derived(absorbNote(seasonList, selected, mode));
  let everySelected = $derived(allSelected(seasonList, selected));
  let subtitle = $derived(modalSubtitle(mediaType, title, year, seasonList));
  let primaryLabel = $derived(submitLabel(mode, mediaType, selected.length));
  let storageSummary = $derived.by(() => {
    const status = system.status;
    const root = status?.storage_root || t('component.addRequest.noStorageRoot');
    return status && status.disk_total_bytes > 0
      ? t('component.addRequest.storageSummary', {
          root,
          free: formatBytes(status.disk_free_bytes),
        })
      : root;
  });
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

  function beginSession() {
    const revision = ++sessionRevision;
    busy = false;
    selected = [];
    seasonList = [];
    loadingSeasons = false;
    profileID = 0;
    searchNow = readSearchOnAdd();
    monitored = initialMonitored ?? false;
    minAvailability = initialAvailability || 'released';
    profiles = null;
    loadingProfiles = false;
    profilesError = null;

    if (mediaType === 'series' && seasons === null) {
      loadingSeasons = true;
      void api
        .discoverTitle('series', tmdbID)
        .then((detail) => {
          if (revision === sessionRevision) seed(detail.seasons ?? []);
        })
        .catch((err) => {
          if (revision !== sessionRevision) return;
          // Without season data the modal still works as a whole-title ask,
          // which is better than refusing to open. Say so rather than hide it.
          pushToast(metadataToast(err, session.isAdmin) ?? errorText(err), 'warning');
          seed([]);
        })
        .finally(() => {
          if (revision === sessionRevision) loadingSeasons = false;
        });
    } else {
      seed(mediaType === 'series' ? (seasons ?? []) : []);
    }

    if (mode === 'add') void loadProfiles(revision);
  }

  $effect(() => {
    mode;
    mediaType;
    tmdbID;
    title;
    year;
    posterPath;
    seasons;
    preselect;
    requestID;
    initialMonitored;
    initialAvailability;
    beginSession();
  });

  async function loadProfiles(revision = sessionRevision) {
    loadingProfiles = true;
    profilesError = null;
    try {
      const loaded = await api.listQualityProfiles();
      if (revision !== sessionRevision) return;
      profiles = loaded;
    } catch (err) {
      if (revision !== sessionRevision) return;
      profiles = null;
      profilesError = errorText(err);
    } finally {
      if (revision === sessionRevision) loadingProfiles = false;
    }
  }

  function setSearchNow(next: boolean) {
    searchNow = next;
    writeSearchOnAdd(next);
  }

  async function submit(andApprove = false) {
    if (!canSubmit) return;
    busy = true;
    try {
      if (mode === 'request') {
        if (andApprove) {
          await requestAndApprove();
        } else {
          await sendRequest();
        }
      } else {
        await addToLibrary();
      }
    } catch (err) {
      // 409 on either path means the view is stale (already in the library, or
      // the request is no longer pending). The server's own words say which.
      // A 503 means the TMDB key is missing or refused — the add cannot work
      // until that is fixed, so the toast names the fix rather than repeating
      // the provider's complaint.
      pushToast(metadataToast(err, session.isAdmin) ?? errorText(err), 'danger');
    } finally {
      busy = false;
    }
  }

  function requestBody(): CreateRequestBody {
    return {
      media_type: mediaType,
      tmdb_id: tmdbID,
      title,
      year,
      poster_path: posterPath,
      ...(mediaType === 'series'
        ? { seasons: requestSeasons(seasonList, selected) }
        : { min_availability: minAvailability }),
    };
  }

  async function sendRequest() {
    const created = await api.createRequest(requestBody());
    pushToast(t('component.addRequest.requested', { title }), 'success');
    ondone?.({ kind: 'requested', request: created });
    onclose();
  }

  /**
   * An admin may decide on their own request on the spot; members only ask.
   * This remains request mode rather than borrowing add mode because the
   * approval intentionally takes safe server defaults — no profile, folder,
   * monitoring, or search choice belongs in that one-click decision.
   */
  async function requestAndApprove() {
    const result = await api.requestAndApprove(requestBody());
    const added = mediaType === 'movie' ? result.movie : result.series;
    if (!added) throw new Error('the approval did not return the added title');
    pushToast(t('component.addRequest.added', { title: added.title }), 'success');
    ondone?.({ kind: 'added', mediaType, libraryID: added.id });
    onclose();
    navigate(mediaType === 'movie' ? `/movies/${added.id}` : `/series/${added.id}`);
  }

  async function addToLibrary() {
    const added = requestID === null ? await addDirect() : await approve();
    // Monitoring and any chosen profile came with the add itself; the series
    // search follows only after that request has returned successfully.
    if (mediaType === 'series' && monitored && searchNow) await api.searchSeriesNow(added.id);
    pushToast(t('component.addRequest.added', { title: added.title }), 'success');
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
          monitored,
          search_now: monitored && searchNow,
          min_availability: minAvailability,
          ...(profileID > 0 ? { quality_profile_id: profileID } : {}),
        })
      : api.addSeries({
          tmdb_id: tmdbID,
          monitored,
          search_missing: false,
          seasons: addSeasons(seasonList, selected),
          ...(profileID > 0 ? { quality_profile_id: profileID } : {}),
        });
  }

  async function approve(): Promise<Movie | Series> {
    const result = await api.approveRequest(
      requestID as number,
      mediaType === 'movie' && monitored && searchNow,
      mediaType === 'series' ? addSeasons(seasonList, selected) : undefined,
      mediaType === 'movie' ? minAvailability : undefined,
      profileID > 0 ? profileID : undefined,
      monitored,
    );
    const added = mediaType === 'movie' ? result.movie : result.series;
    if (!added) throw new Error('the approval did not return the added title');
    return added;
  }
</script>

<Modal
  title={mode === 'add' ? t('component.addItem.title') : t('component.addRequest.request')}
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
            <span class="micro-label">{t('component.addRequest.seasons')}</span>
            {#if selectable.length > 0}
              <button
                type="button"
                class="ml-auto text-sm text-accent-text transition-colors duration-150 ease-out hover:text-accent"
                onclick={() => (selected = everySelected ? [] : allSeasonNumbers(seasonList))}>
                {everySelected ? t('component.addRequest.deselectAll') : t('component.actions.selectAll')}
              </button>
            {/if}
          </div>

          <ul class="flex flex-col divide-y divide-border overflow-hidden rounded-md border border-border">
            {#each seasonList as season (season.season_number)}
              {@const meta = seasonMeta(season)}
              {@const checked = selected.includes(season.season_number)}
              <li>
                <label
                  class="flex min-h-12 cursor-pointer flex-wrap items-center gap-3 px-3 py-2 transition-colors duration-150 ease-out hover:bg-raised
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
                  <span class="ml-auto shrink-0">
                    {#if season.in_library}
                      <Badge tone="success">{t('component.status.inLibrary')}</Badge>
                    {:else if season.requested}
                      <Badge tone="warning">{t('component.status.requested')}</Badge>
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
        <Field label={t('component.addRequest.minimumAvailability')} for="add-availability">
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
        {#if loadingProfiles}
          <div class="flex flex-col gap-2" aria-label={t('component.addRequest.loadingProfiles')}>
            <span class="micro-label">{t('component.addRequest.profile')}</span>
            <Skeleton class="h-9 w-full rounded-sm" />
          </div>
        {:else if profilesError}
          <LoadError message={profilesError} onretry={() => void loadProfiles()} />
        {:else if profiles !== null && profiles.length === 0}
          <div class="flex flex-col gap-1 rounded-sm border border-border bg-raised px-3 py-2">
            <p class="text-sm text-ink-secondary">{t('component.addRequest.noProfiles')}</p>
            <a href="/settings/quality-profiles" class="text-sm text-accent-text hover:underline">
              {t('component.addRequest.manageProfiles')}
            </a>
          </div>
        {:else if profiles}
          <Field label={t('component.addRequest.profile')} for="add-profile">
            <select id="add-profile" bind:value={profileID} class={SELECT_CLASS}>
              <option value={0}>{t('component.addRequest.libraryDefault')}</option>
              {#each profiles as profile (profile.id)}
                <option value={profile.id}>{profile.name}</option>
              {/each}
            </select>
          </Field>
        {/if}

        <!-- Caravan has one storage root, so the folder is shown rather than
             chosen: a picker with a single option is a lie about the model. -->
        <Field label={t('component.addRequest.rootFolder')} for="add-root">
          <select id="add-root" class={SELECT_CLASS} title={storageSummary} disabled>
            <option>{storageSummary}</option>
          </select>
        </Field>
      </div>

      <div class="flex flex-col gap-2">
        <label class="flex items-center gap-3 rounded-md border border-border bg-raised px-3 py-2">
          <input
            id="add-monitored"
            type="checkbox"
            checked={monitored}
            onchange={(event) => (monitored = event.currentTarget.checked)}
            class="size-4 accent-accent" />
          <span class="text-base text-ink">{t('component.addRequest.addAndMonitor')}</span>
        </label>
        {#if monitored}
          <label class="ml-6 flex items-center gap-3 rounded-md border border-border bg-raised px-3 py-2">
            <input
              type="checkbox"
              checked={searchNow}
              onchange={(event) => setSearchNow(event.currentTarget.checked)}
              class="size-4 accent-accent" />
            <span class="text-base text-ink">
              {mediaType === 'series'
                ? t('component.addRequest.searchSeasonsNow')
                : t('component.addRequest.searchMovieNow')}
            </span>
          </label>
        {/if}
      </div>
    {/if}
  </div>

  {#snippet footer()}
    <Button variant="secondary" disabled={busy} onclick={onclose}>{t('component.actions.cancel')}</Button>
    {#if mode === 'request' && session.isAdmin}
      <Button variant="secondary" disabled={!canSubmit} onclick={() => void submit(true)}>
        {busy ? t('component.actions.working') : t('component.addRequest.approve')}
      </Button>
    {/if}
    <Button variant="primary" disabled={!canSubmit} onclick={() => void submit()}>
      {busy ? t('component.actions.working') : primaryLabel}
    </Button>
  {/snippet}
</Modal>
