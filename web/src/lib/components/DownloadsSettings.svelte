<script lang="ts">
  /**
   * Settings → Downloads: everything that actually pulls a release down, in the
   * order the product means it. The built-in engines come first and external
   * clients last, so the screen reads as "this already works" rather than as a
   * list of things still to configure.
   */
  import type { Settings } from '../api/types';
  import { useI18n } from '../i18n.svelte';
  import ConcurrencySettings from './ConcurrencySettings.svelte';
  import DownloadClientSettings from './DownloadClientSettings.svelte';
  import EngineSettings from './EngineSettings.svelte';
  import RemotePathMappings from './RemotePathMappings.svelte';
  import UsenetServerSettings from './UsenetServerSettings.svelte';

  interface Props {
    settings: Settings;
    saving?: boolean;
    /** The shell owns the persisted preference; this component applies it semantically. */
    showAdvanced?: boolean;
    onsave: (patch: Settings, note: string) => Promise<boolean>;
  }

  let { settings, saving = false, showAdvanced = false, onsave }: Props = $props();
  const { t } = useI18n();
</script>

<div class="flex flex-col gap-5">
  <section id="download-concurrency">
    <ConcurrencySettings
      {settings}
      {saving}
      onsave={(patch) => onsave(patch, t('component.downloadsSettings.concurrencySaved'))} />
  </section>
  <section id="torrent-engine" data-settings-advanced hidden={!showAdvanced} aria-hidden={!showAdvanced}>
    <EngineSettings
      {settings}
      {saving}
      onsave={(patch) => onsave(patch, t('component.downloadsSettings.engineSaved'))} />
  </section>
  <section id="usenet-servers">
    <UsenetServerSettings />
  </section>
  <section id="download-clients">
    <DownloadClientSettings />
  </section>
  <section id="remote-path-mappings">
    <RemotePathMappings />
  </section>
</div>
