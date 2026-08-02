<script lang="ts">
  /** Settings for general configuration, indexers, engine defaults, profiles and storage. */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import { SETTING_TMDB_API_KEY, type Settings } from '../api/types';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import Icon from '../components/Icon.svelte';
  import DlnaSettings from '../components/DlnaSettings.svelte';
  import DownloadClientSettings from '../components/DownloadClientSettings.svelte';
  import EngineSettings from '../components/EngineSettings.svelte';
  import IndexerSettings from '../components/IndexerSettings.svelte';
  import JellyfinSettings from '../components/JellyfinSettings.svelte';
  import LoadError from '../components/LoadError.svelte';
  import PageTabs from '../components/PageTabs.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import TextInput from '../components/TextInput.svelte';
  import QualityProfiles from '../components/QualityProfiles.svelte';
  import SecuritySettings from '../components/SecuritySettings.svelte';
  import StorageSettings from '../components/StorageSettings.svelte';
  import TVProfileSettings from '../components/TVProfileSettings.svelte';
  import UsenetServerSettings from '../components/UsenetServerSettings.svelte';
  import { UNKNOWN } from '../format';
  import { pushToast } from '../state/toast.svelte';
  import { system } from '../state/system.svelte';

  type Tab =
    | 'general'
    | 'indexers'
    | 'download-clients'
    | 'usenet-servers'
    | 'engine'
    | 'quality-profiles'
    | 'tv-profile'
    | 'dlna'
    | 'jellyfin'
    | 'security'
    | 'storage';

  const TABS: { key: Tab; label: string }[] = [
    { key: 'general', label: 'General' },
    { key: 'indexers', label: 'Indexers' },
    { key: 'download-clients', label: 'Download clients' },
    { key: 'usenet-servers', label: 'Usenet servers' },
    { key: 'engine', label: 'Engine' },
    { key: 'quality-profiles', label: 'Quality profiles' },
    { key: 'tv-profile', label: 'TV profile' },
    { key: 'dlna', label: 'DLNA' },
    { key: 'jellyfin', label: 'Jellyfin' },
    { key: 'security', label: 'Security' },
    { key: 'storage', label: 'Storage' },
  ];

  let tab = $state<Tab>('general');
  let settings = $state<Settings | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let saving = $state(false);

  let tmdbKey = $state('');

  async function load() {
    loading = true;
    try {
      const loaded = await api.getSettings();
      settings = loaded;
      tmdbKey = loaded[SETTING_TMDB_API_KEY] ?? '';
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  async function save(patch: Settings, note: string): Promise<boolean> {
    saving = true;
    try {
      settings = await api.putSettings(patch);
      await system.refresh();
      pushToast(note, 'success');
      return true;
    } catch (err) {
      pushToast(errorText(err), 'danger');
      return false;
    } finally {
      saving = false;
    }
  }

  let status = $derived(system.status);
</script>

<div
  class="flex flex-col gap-6 {tab === 'indexers' ||
  tab === 'download-clients' ||
  tab === 'usenet-servers' ||
  tab === 'quality-profiles'
    ? 'max-w-5xl'
    : 'max-w-3xl'}">
  <PageTabs
    tabs={TABS}
    active={tab}
    onchange={(key) => (tab = key)}
    ariaLabel="Settings sections" />

  <!-- These own their fetches, so they render whether or not /settings loaded. -->
  {#if tab === 'indexers'}
    <IndexerSettings />
  {:else if tab === 'download-clients'}
    <DownloadClientSettings />
  {:else if tab === 'usenet-servers'}
    <UsenetServerSettings />
  {:else if tab === 'quality-profiles'}
    <QualityProfiles />
  {:else if tab === 'jellyfin'}
    <JellyfinSettings />
  {:else if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && settings === null}
    <div class="flex flex-col gap-4">
      <Skeleton class="h-4 w-32" />
      <Skeleton class="h-9 w-full" />
      <Skeleton class="h-8 w-24" />
    </div>
  {:else if tab === 'security' && settings}
    <SecuritySettings {settings} />
  {:else if tab === 'dlna' && settings}
    <DlnaSettings
      {settings}
      {saving}
      onsave={(patch) => save(patch, 'DLNA settings saved.')} />
  {:else if tab === 'tv-profile' && settings}
    <TVProfileSettings
      {settings}
      {saving}
      onsave={(patch) => save(patch, 'TV profile saved.')} />
  {:else if tab === 'engine' && settings}
    <EngineSettings
      {settings}
      {saving}
      onsave={(patch) => save(patch, 'Engine settings saved.')} />
  {:else if tab === 'general'}
    <section class="flex flex-col gap-6">
      <Field
        label="TMDB API key"
        for="tmdb-key"
        help="Caravan uses TMDB for titles, artwork and episode data. The key is stored in the database, never in caravan.yaml or logs.">
        <TextInput id="tmdb-key" bind:value={tmdbKey} type="password" mono placeholder="•••••" />
      </Field>

      <Button
        variant="primary"
        disabled={saving}
        class="self-start"
        onclick={() => save({ [SETTING_TMDB_API_KEY]: tmdbKey.trim() }, 'TMDB API key saved.')}>
        <Icon name="check" size={14} />
        {saving ? 'Saving…' : 'Save'}
      </Button>

      <dl class="grid grid-cols-2 gap-4 rounded-md border border-border bg-surface p-4 sm:grid-cols-4">
        <div>
          <dt class="micro-label">Version</dt>
          <dd class="mt-1 font-mono text-sm text-ink">{status?.version || UNKNOWN}</dd>
        </div>
        <div>
          <dt class="micro-label">Mode</dt>
          <dd class="mt-1 font-mono text-sm text-ink">{status?.mode || UNKNOWN}</dd>
        </div>
        <div>
          <dt class="micro-label">Schema</dt>
          <dd class="mt-1 font-mono text-sm text-ink">
            {status ? `v${status.schema_version}` : UNKNOWN}
          </dd>
        </div>
        <div>
          <dt class="micro-label">Library files</dt>
          <dd class="mt-1 font-mono text-sm text-ink">
            {status ? status.counts.media_files : UNKNOWN}
          </dd>
        </div>
      </dl>
    </section>
  {:else if settings}
    <!-- Storage owns two operations with very different consequences, so it
         owns its own component and its own migration polling. -->
    <StorageSettings {settings} />
  {/if}
</div>
