<script lang="ts">
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Library, LibraryKind, QualityProfile } from '../api/types';
  import { pushToast } from '../state/toast.svelte';

  interface Props {
    /** `0` deliberately means this item does not override its library. */
    profileID: number;
    kind: Extract<LibraryKind, 'movie' | 'tv'>;
    /** Persist an item override. Rejecting restores the select's stored value. */
    onassign: (profileID: number) => Promise<void>;
  }

  let { profileID, kind, onassign }: Props = $props();

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
  let inheritedSource = $derived(library?.quality_profile_id ? library.name : 'system default');
  let explicitProfile = $derived(profiles.find((profile) => profile.id === profileID) ?? null);

  function profileName(profile: QualityProfile | null): string {
    return profile?.name ?? 'Unknown profile';
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
  <dt class="micro-label">Quality profile</dt>
  <dd class="mt-1 flex min-w-0 flex-col gap-1.5">
    {#if loading}
      <span class="text-sm text-ink-secondary">Loading profile choices…</span>
    {:else if loadError}
      <div class="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-danger" role="alert">
        <span>Could not load profile choices: {loadError}</span>
        <button
          type="button"
          class="font-medium text-danger underline underline-offset-2 hover:text-ink focus:outline-none focus:ring-2 focus:ring-accent"
          onclick={load}>
          Retry
        </button>
      </div>
    {:else}
      <select
        aria-label="Quality profile"
        aria-describedby="profile-assignment-state"
        value={String(profileID)}
        disabled={saving}
        onchange={assign}
        class="h-8 w-full rounded-sm border border-border-strong bg-raised px-2 text-sm text-ink
               focus:border-accent focus:outline-none disabled:cursor-wait disabled:opacity-50">
        <option value="0">Inherit</option>
        {#each profiles as profile (profile.id)}
          <option value={String(profile.id)}>{profile.name}</option>
        {/each}
      </select>

      <p id="profile-assignment-state" class="text-sm text-ink-secondary" aria-live="polite">
        {#if profileID === 0}
          Inherited from {inheritedSource}: <span class="font-medium text-ink">{profileName(inheritedProfile)}</span>
        {:else}
          Override: <span class="font-medium text-ink">{profileName(explicitProfile)}</span>
        {/if}
      </p>
    {/if}
  </dd>
</div>
