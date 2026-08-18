<script lang="ts" module>
  import type { MinAvailability } from '../api/types';

  export interface MediaEditValues {
    monitored: boolean;
    qualityProfileID: number;
    minAvailability?: MinAvailability;
  }
</script>

<script lang="ts">
  /** One editor for mutable fields on movie, series, anime, and adult details. */
  import { onMount, untrack } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Library, MinAvailability, QualityProfile } from '../api/types';
  import { AVAILABILITY_OPTIONS, availabilityHint } from '../discover';
  import { useI18n } from '../i18n.svelte';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import Modal from './Modal.svelte';
  import Toggle from './Toggle.svelte';

  interface Props {
    title: string;
    kind: 'movie' | 'series' | 'adult';
    libraryID: number;
    monitored: boolean;
    qualityProfileID: number;
    minAvailability?: MinAvailability;
    onsave: (values: MediaEditValues) => Promise<void>;
    onclose: () => void;
  }

  let {
    title,
    kind,
    libraryID,
    monitored,
    qualityProfileID,
    minAvailability,
    onsave,
    onclose,
  }: Props = $props();

  const { t } = useI18n();
  const fieldID = $props.id();
  const profileSelectID = `${fieldID}-profile`;
  const availabilitySelectID = `${fieldID}-availability`;
  const initial = untrack(() => ({ monitored, qualityProfileID, minAvailability }));

  let draft = $state({ ...initial });
  let profiles = $state<QualityProfile[]>([]);
  let libraries = $state<Library[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let saveError = $state<string | null>(null);
  let saving = $state(false);

  let dirty = $derived(
    draft.monitored !== initial.monitored ||
      draft.qualityProfileID !== initial.qualityProfileID ||
      draft.minAvailability !== initial.minAvailability,
  );
  let library = $derived(libraries.find((candidate) => candidate.id === libraryID) ?? null);
  let systemDefault = $derived(profiles.find((profile) => profile.is_default) ?? null);
  let libraryProfile = $derived(
    library?.quality_profile_id
      ? profiles.find((profile) => profile.id === library.quality_profile_id) ?? null
      : null,
  );
  let inheritedProfile = $derived(libraryProfile ?? systemDefault);
  let explicitProfile = $derived(
    profiles.find((profile) => profile.id === draft.qualityProfileID) ?? null,
  );
  let selectedProfile = $derived(
    draft.qualityProfileID > 0
      ? explicitProfile ?? inheritedProfile
      : inheritedProfile,
  );
  let inheritedSource = $derived(
    libraryProfile
      ? library?.name ?? t('component.editMedia.libraryDefault')
      : t('component.editMedia.systemDefault'),
  );
  let target = $derived(selectedProfile?.tv_profile ?? 'safe');
  let targetName = $derived(
    target === 'capable'
      ? t('component.editMedia.capableTarget')
      : t('component.editMedia.safeTarget'),
  );
  let targetDescription = $derived(
    target === 'capable'
      ? t('component.editMedia.capableTargetHelp')
      : t('component.editMedia.safeTargetHelp'),
  );

  async function loadChoices() {
    loading = true;
    try {
      [profiles, libraries] = await Promise.all([
        api.listQualityProfiles(),
        api.listLibraries(),
      ]);
      loadError = null;
    } catch (err) {
      loadError = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(loadChoices);

  async function save() {
    if (!dirty || saving) return;
    saving = true;
    saveError = null;
    try {
      await onsave({
        monitored: draft.monitored,
        qualityProfileID: draft.qualityProfileID,
        ...(kind === 'movie' && draft.minAvailability
          ? { minAvailability: draft.minAvailability }
          : {}),
      });
      onclose();
    } catch (err) {
      saveError = errorText(err);
    } finally {
      saving = false;
    }
  }
</script>

<Modal
  title={t('component.editMedia.title', { title })}
  width="max-w-xl"
  {dirty}
  {onclose}>
  <div class="flex flex-col gap-5 p-4 sm:p-5">
    {#if loading}
      <p class="text-sm text-ink-secondary">{t('component.editMedia.loading')}</p>
    {:else if loadError}
      <div class="rounded-md border border-danger/40 bg-danger/10 p-3" role="alert">
        <p class="text-sm text-danger">
          {t('component.editMedia.loadError', { message: loadError })}
        </p>
        <Button class="mt-3" size="sm" variant="secondary" onclick={loadChoices}>
          {t('component.actions.retry')}
        </Button>
      </div>
    {:else}
      <Field
        label={t('component.editMedia.monitoring')}
        help={kind === 'movie'
          ? t('component.editMedia.monitorMovieHelp')
          : kind === 'adult'
            ? t('component.editMedia.monitorAdultHelp')
            : t('component.editMedia.monitorSeriesHelp')}>
        <Toggle
          checked={draft.monitored}
          label={t('component.editMedia.monitored')}
          onchange={(next) => (draft.monitored = next)} />
      </Field>

      {#if kind === 'movie' && draft.minAvailability}
        <Field
          label={t('component.editMedia.minimumAvailability')}
          for={availabilitySelectID}
          help={availabilityHint(draft.minAvailability)}>
          <select
            id={availabilitySelectID}
            aria-label={t('component.editMedia.minimumAvailability')}
            value={draft.minAvailability}
            onchange={(event) =>
              (draft.minAvailability = event.currentTarget.value as MinAvailability)}
            class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md
                   text-ink focus:border-accent focus:outline-none">
            {#each AVAILABILITY_OPTIONS as option (option.value)}
              <option value={option.value}>{option.label}</option>
            {/each}
          </select>
        </Field>
      {/if}

      <Field
        label={t('component.editMedia.qualityProfile')}
        for={profileSelectID}
        help={draft.qualityProfileID === 0
          ? t('component.editMedia.inheritedProfile', {
              source: inheritedSource,
              profile: inheritedProfile?.name ?? t('component.editMedia.unknownProfile'),
            })
          : t('component.editMedia.profileOverride', {
              profile: explicitProfile?.name ?? t('component.editMedia.unknownProfile'),
            })}>
        <select
          id={profileSelectID}
          aria-label={t('component.editMedia.qualityProfile')}
          bind:value={draft.qualityProfileID}
          class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md
                 text-ink focus:border-accent focus:outline-none">
          <option value={0}>{t('component.editMedia.inherit')}</option>
          {#each profiles as profile (profile.id)}
            <option value={profile.id}>{profile.name}</option>
          {/each}
        </select>
      </Field>

      <section
        class="rounded-md border border-border bg-surface p-3"
        aria-labelledby={`${fieldID}-playback-target`}>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h3 id={`${fieldID}-playback-target`} class="micro-label">
            {t('component.editMedia.playbackTarget')}
          </h3>
          <Badge mono>{targetName}</Badge>
        </div>
        <p class="mt-2 text-sm text-ink">{targetDescription}</p>
        <p class="mt-1 text-sm text-ink-secondary">
          {t('component.editMedia.playbackTargetSource', {
            profile: selectedProfile?.name ?? t('component.editMedia.unknownProfile'),
          })}
          <a
            href="/settings/quality-profiles"
            class="ml-1 text-accent-text underline-offset-2 hover:underline">
            {t('component.editMedia.manageProfiles')}
          </a>
        </p>
      </section>

      {#if saveError}
        <p class="text-sm text-danger" role="alert">{saveError}</p>
      {/if}
    {/if}
  </div>

  {#snippet footer()}
    <Button variant="ghost" disabled={saving} onclick={onclose}>
      {t('component.actions.cancel')}
    </Button>
    <Button
      variant="primary"
      disabled={loading || Boolean(loadError) || saving || !dirty}
      onclick={save}>
      {saving ? t('component.editMedia.saving') : t('component.actions.saveChanges')}
    </Button>
  {/snippet}
</Modal>
