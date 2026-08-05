<script lang="ts">
  /**
   * Settings → Playback: the ways the library reaches a screen. DLNA is built
   * in and comes first; Jellyfin is a handoff, and Stash is the same handoff
   * for the adult library. The TV profile is the compatibility target; output
   * settings only control how any required re-encoding runs.
   */
  import type { Settings } from '../api/types';
  import DlnaSettings from './DlnaSettings.svelte';
  import ConversionSettings from './ConversionSettings.svelte';
  import JellyfinSettings from './JellyfinSettings.svelte';
  import StashSettings from './StashSettings.svelte';
  import TVProfileSettings from './TVProfileSettings.svelte';
  import { session } from '../state/session.svelte';

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
  <!-- The gate is on the render, not inside the card, and for the reason the
       adult routes are gated on render in App.svelte: an ungranted browser must
       not put GET /adult/stash on the wire at all. `session.adult` reads false
       until /auth/me answers, so the pane starts without it. -->
  {#if session.adult}
    <StashSettings />
  {/if}
  <TVProfileSettings {settings} {saving} onsave={(patch) => onsave(patch, 'TV profile saved.')} />
  <ConversionSettings
    {settings}
    {saving}
    onsave={(patch) => onsave(patch, 'Conversion settings saved.')} />
</div>
