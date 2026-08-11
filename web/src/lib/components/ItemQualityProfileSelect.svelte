<script lang="ts">
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Library, LibraryKind, QualityProfile } from '../api/types';
  import { useI18n } from '../i18n.svelte';
  import { pushToast } from '../state/toast.svelte';

  interface Props {
    /** `0` deliberately means this item does not override its library. */
    profileID: number;
    kind: Extract<LibraryKind, 'movie' | 'tv'>;
    /** Persist an item override. Rejecting restores the select's stored value. */
    onassign: (profileID: number) => Promise<void>;
  }

  let { profileID, kind, onassign }: Props = $props();
  const { t } = useI18n();

  const fieldID = $props.id();
  const selectID = `${fieldID}-select`;
  const stateID = `${fieldID}-state`;

  let profiles = $state<QualityProfile[]>([]);
  let libraries = $state<Library[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let saving = $state(false);

  async function load() {
    loading = true;
    try {
      const [loadedProfiles, loadedLibraries] = await Promise.all([
        api.listQualityProfiles(),
        api.listLibraries(),
      ]);
      profiles = loadedProfiles;
      libraries = loadedLibraries;
      loadError = null;
    } catch (err) {
      loadError = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  let library = $derived(libraries.find((candidate) => candidate.kind === kind) ?? null);
  let systemDefault = $derived(profiles.find((profile) => profile.is_default) ?? null);
  let inheritedProfile = $derived(
    library?.quality_profile_id
      ? profiles.find((profile) => profile.id === library.quality_profile_id) ?? null
      : systemDefault,
  );
  let inheritedSource = $derived(library?.quality_profile_id ? library.name : t('component.itemQualityProfile.systemDefault'));
  let explicitProfile = $derived(profiles.find((profile) => profile.id === profileID) ?? null);

  function profileName(profile: QualityProfile | null): string {
    return profile?.name ?? t('component.itemQualityProfile.unknown');
  }

  async function assign(event: Event) {
    const select = event.currentTarget as HTMLSelectElement;
    const next = Number(select.value);
    if (next === profileID || saving) return;

    const previous = String(profileID);
    saving = true;
    try {
      await onassign(next);
    } catch (err) {
      // The detail item remains unchanged until the API confirms the PATCH. The
      // native select has already moved, so put it back to that stored value.
      select.value = previous;
      pushToast(errorText(err), 'danger');
    } finally {
      saving = false;
    }
  }
</script>

<div class="min-w-0">
  <dt class="micro-label"><label for={selectID}>{t('component.itemQualityProfile.label')}</label></dt>
  <dd class="mt-1 flex min-w-0 flex-col gap-1.5">
    {#if loading}
      <span class="text-sm text-ink-secondary">{t('component.itemQualityProfile.loading')}</span>
    {:else if loadError}
      <div class="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-danger" role="alert">
        <span>{t('component.itemQualityProfile.loadError', { message: loadError })}</span>
        <button
          type="button"
          class="font-medium text-danger underline underline-offset-2 hover:text-ink focus:outline-none focus:ring-2 focus:ring-accent"
          onclick={load}>
          {t('component.itemQualityProfile.retry')}
        </button>
      </div>
    {:else}
      <select
        id={selectID}
        aria-label={t('component.itemQualityProfile.label')}
        aria-describedby={stateID}
        value={String(profileID)}
        disabled={saving}
        onchange={assign}
        class="h-8 w-full rounded-sm border border-border-strong bg-raised px-2 text-sm text-ink
               focus:border-accent focus:outline-none disabled:cursor-wait disabled:opacity-50">
        <option value="0">{t('component.itemQualityProfile.inherit')}</option>
        {#each profiles as profile (profile.id)}
          <option value={String(profile.id)}>{profile.name}</option>
        {/each}
      </select>

      <p id={stateID} class="text-sm text-ink-secondary" aria-live="polite">
        {#if profileID === 0}
          {t('component.itemQualityProfile.inherited', {
            source: inheritedSource,
            profile: profileName(inheritedProfile),
          })}
        {:else}
          {t('component.itemQualityProfile.override', { profile: profileName(explicitProfile) })}
        {/if}
      </p>
    {/if}
  </dd>
</div>
