<script lang="ts">
  /**
   * Settings → Downloads: everything that actually pulls a release down, in the
   * order the product means it. The built-in engines come first and external
   * clients last, so the screen reads as "this already works" rather than as a
   * list of things still to configure.
   */
  import type { Settings } from '../api/types';
  import ConcurrencySettings from './ConcurrencySettings.svelte';
  import DownloadClientSettings from './DownloadClientSettings.svelte';
  import EngineSettings from './EngineSettings.svelte';
  import UsenetServerSettings from './UsenetServerSettings.svelte';

  interface Props {
    settings: Settings;
    saving?: boolean;
    onsave: (patch: Settings, note: string) => Promise<boolean>;
  }

  let { settings, saving = false, onsave }: Props = $props();
</script>

<div class="flex flex-col gap-5">
  <ConcurrencySettings
    {settings}
    {saving}
    onsave={(patch) => onsave(patch, 'Concurrency limits saved.')} />
  <EngineSettings
    {settings}
    {saving}
    onsave={(patch) => onsave(patch, 'Engine settings saved.')} />
  <UsenetServerSettings />
  <DownloadClientSettings />
</div>
