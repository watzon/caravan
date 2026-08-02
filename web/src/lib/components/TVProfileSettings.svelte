<script lang="ts">
  /**
   * The active TV profile (SPEC §8): a description of the set the library is
   * played back on, not a filter. Choosing one changes what the release picker
   * and the file rows warn about; it never changes what Caravan grabs.
   *
   * The presets themselves are code-owned (GET /tv-profiles); the only thing
   * stored is which one is active, under the `tv_profile` settings key.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import { SETTING_TV_PROFILE, type Settings, type TVProfile } from '../api/types';
  import Badge from './Badge.svelte';
  import Banner from './Banner.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import LoadError from './LoadError.svelte';
  import SettingsCard from './SettingsCard.svelte';
  import Skeleton from './Skeleton.svelte';

  interface Props {
    settings: Settings;
    saving?: boolean;
    onsave: (patch: Settings) => Promise<boolean>;
  }

  let { settings, saving = false, onsave }: Props = $props();

  let profiles = $state<TVProfile[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let selected = $state('');

  async function load() {
    loading = true;
    try {
      const found = await api.listTVProfiles();
      profiles = found;
      // The server owns the fallback, so the active flag is the truth about
      // what the flags on screen were computed against — not the raw setting,
      // which may be absent.
      selected =
        settings[SETTING_TV_PROFILE] || found.find((p) => p.active)?.id || found[0]?.id || '';
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  function summary(profile: TVProfile): string[] {
    const parts: string[] = [];
    if (profile.video_codecs.length > 0) {
      parts.push(profile.video_codecs.join('/').toUpperCase());
    }
    if (profile.max_bit_depth > 0) parts.push(`${profile.max_bit_depth}-bit`);
    if (profile.audio_codecs.length > 0) parts.push(profile.audio_codecs.join('/'));
    if (profile.containers.length > 0) parts.push(profile.containers.join('/').toUpperCase());
    if (profile.max_quality) parts.push(`≤ ${profile.max_quality}`);
    return parts;
  }
</script>

<SettingsCard
  title="TV profile"
  description="What the TV on the other end can decode. Search warns before you grab — it never hides a release.">
  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && profiles === null}
    <div class="flex flex-col gap-3">
      <Skeleton class="h-20 w-full" />
      <Skeleton class="h-20 w-full" />
    </div>
  {:else if profiles}
    <fieldset class="flex flex-col gap-3">
      <legend class="micro-label mb-2">Target set</legend>
      {#each profiles as profile (profile.id)}
        <label
          class="flex cursor-pointer gap-3 rounded-md border p-4 transition-colors duration-150
                 {selected === profile.id
            ? 'border-accent bg-accent-tint'
            : 'border-border hover:bg-raised'}">
          <input
            type="radio"
            name="tv-profile"
            class="mt-1 accent-accent"
            value={profile.id}
            checked={selected === profile.id}
            onchange={() => (selected = profile.id)} />
          <span class="flex min-w-0 flex-col gap-2">
            <span class="text-sm font-semibold text-ink">{profile.name}</span>
            <span class="text-base text-ink-secondary">{profile.description}</span>
            <span class="flex flex-wrap gap-1.5">
              {#each summary(profile) as part (part)}
                <Badge mono>{part}</Badge>
              {/each}
            </span>
          </span>
        </label>
      {/each}
    </fieldset>

    <Button
      variant="primary"
      class="self-start"
      disabled={saving || selected === ''}
      onclick={() => onsave({ [SETTING_TV_PROFILE]: selected })}>
      <Icon name="check" size={14} />
      {saving ? 'Saving…' : 'Save'}
    </Button>

    <Banner
      tone="info"
      icon="warning"
      title="DTS is flagged on every profile"
      message="Current Samsung sets cannot decode DTS at all and it is flaky elsewhere, so a DTS release is called out whichever profile is active." />
  {/if}
</SettingsCard>
