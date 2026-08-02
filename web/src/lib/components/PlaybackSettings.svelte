<script lang="ts">
  /**
   * Settings → Playback: the three ways the library reaches a screen. DLNA is
   * built in and comes first; Jellyfin is a handoff; the TV profile only
   * changes what search warns about.
   */
  import type { Settings } from '../api/types';
  import DlnaSettings from './DlnaSettings.svelte';
  import JellyfinSettings from './JellyfinSettings.svelte';
  import TVProfileSettings from './TVProfileSettings.svelte';

  interface Props {
    settings: Settings;
    saving?: boolean;
    onsave: (patch: Settings, note: string) => Promise<boolean>;
  }

  let { settings, saving = false, onsave }: Props = $props();
</script>

<div class="flex flex-col gap-5">
  <DlnaSettings {settings} {saving} onsave={(patch) => onsave(patch, 'DLNA settings saved.')} />
  <JellyfinSettings />
  <TVProfileSettings {settings} {saving} onsave={(patch) => onsave(patch, 'TV profile saved.')} />
</div>
