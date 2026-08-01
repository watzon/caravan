<script lang="ts">
  /** Settings for general configuration, indexers, engine defaults, profiles and storage. */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import { SETTING_STORAGE_ROOT, SETTING_TMDB_API_KEY, type Settings } from '../api/types';
  import Banner from '../components/Banner.svelte';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import Icon from '../components/Icon.svelte';
  import EngineSettings from '../components/EngineSettings.svelte';
  import IndexerSettings from '../components/IndexerSettings.svelte';
  import LoadError from '../components/LoadError.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import TextInput from '../components/TextInput.svelte';
  import QualityProfiles from '../components/QualityProfiles.svelte';
  import { UNKNOWN } from '../format';
  import { pushToast } from '../state/toast.svelte';
  import { system } from '../state/system.svelte';

  type Tab = 'general' | 'indexers' | 'engine' | 'quality-profiles' | 'storage';

  const TABS: { key: Tab; label: string }[] = [
    { key: 'general', label: 'General' },
    { key: 'indexers', label: 'Indexers' },
    { key: 'engine', label: 'Engine' },
    { key: 'quality-profiles', label: 'Quality profiles' },
    { key: 'storage', label: 'Storage' },
  ];

  let tab = $state<Tab>('general');
  let settings = $state<Settings | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let saving = $state(false);
  let scanning = $state(false);

  let tmdbKey = $state('');
  let storageRoot = $state('');

  async function load() {
    loading = true;
    try {
      const loaded = await api.getSettings();
      settings = loaded;
      tmdbKey = loaded[SETTING_TMDB_API_KEY] ?? '';
      storageRoot = loaded[SETTING_STORAGE_ROOT] ?? '';
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

  async function rescan() {
    scanning = true;
    try {
      await api.rescan();
      const summary = await api.awaitScan();
      await system.refresh();
      pushToast(
        `Scan finished: ${summary.media_files} files in the library, ${summary.unmatched} unmatched.`,
        summary.unmatched > 0 ? 'warning' : 'success',
      );
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      scanning = false;
    }
  }

  let status = $derived(system.status);
</script>

<div class="flex flex-col gap-6 {tab === 'indexers' || tab === 'quality-profiles' ? 'max-w-5xl' : 'max-w-3xl'}">
  <div class="flex gap-2 border-b border-border" role="tablist" aria-label="Settings sections">
    {#each TABS as item (item.key)}
      <button
        type="button"
        role="tab"
        aria-selected={tab === item.key}
        onclick={() => (tab = item.key)}
        class="-mb-px border-b-2 px-3 py-2 text-base transition-colors duration-150 ease-out
               {tab === item.key
          ? 'border-accent text-accent-text'
          : 'border-transparent text-ink-secondary hover:text-ink'}">
        {item.label}
      </button>
    {/each}
  </div>

  <!-- Indexers and quality profiles own their fetches, so they render whether or not /settings loaded. -->
  {#if tab === 'indexers'}
    <IndexerSettings />
  {:else if tab === 'quality-profiles'}
    <QualityProfiles />
  {:else if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && settings === null}
    <div class="flex flex-col gap-4">
      <Skeleton class="h-4 w-32" />
      <Skeleton class="h-9 w-full" />
      <Skeleton class="h-8 w-24" />
    </div>
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
  {:else}
    <section class="flex flex-col gap-6">
      <Field
        label="Storage root"
        for="settings-storage-root"
        help="Every path in the database is relative to this folder, so re-pointing is instant and safe - the files stay where they are.">
        <TextInput id="settings-storage-root" bind:value={storageRoot} mono placeholder="/data" />
      </Field>

      <div class="flex flex-wrap gap-2">
        <Button
          variant="primary"
          disabled={saving || storageRoot.trim() === ''}
          onclick={() =>
            save({ [SETTING_STORAGE_ROOT]: storageRoot.trim() }, 'Storage root re-pointed.')}>
          <Icon name="check" size={14} />
          {saving ? 'Saving…' : 'Re-point'}
        </Button>
        <Button variant="secondary" disabled={scanning} onclick={rescan}>
          <Icon name="refresh" size={14} />
          {scanning ? 'Scanning…' : 'Rescan library'}
        </Button>
      </div>

      <Banner
        tone="info"
        icon="warning"
        title="Moving files is a later phase"
        message="Re-pointing changes where Caravan looks; it never moves media. The migrate operation that relocates the library arrives with the deployment phase." />
    </section>
  {/if}
</div>
