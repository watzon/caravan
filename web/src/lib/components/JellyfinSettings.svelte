<script lang="ts">
  /**
   * The Jellyfin playback handoff (SPEC §5.2, PLAN phase 4 task 1).
   *
   * Caravan already writes the library in the layout and NFO conventions
   * Jellyfin reads, so this integration is one thing only: after an import,
   * tell Jellyfin to look again. There is no scan button here on purpose — the
   * scan is queued by the import pipeline and run by the job queue, so a manual
   * one would be a second, weaker path to the same effect.
   *
   * The component owns its fetch, like IndexerSettings: the three values are
   * validated and tested together, so they have their own endpoints rather than
   * riding on /settings.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { JellyfinConfig } from '../api/types';
  import Banner from './Banner.svelte';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';
  import LoadError from './LoadError.svelte';
  import SettingsCard from './SettingsCard.svelte';
  import Skeleton from './Skeleton.svelte';
  import TextInput from './TextInput.svelte';
  import Toggle from './Toggle.svelte';
  import { pushToast } from '../state/toast.svelte';

  interface TestResult {
    ok: boolean;
    message: string;
  }

  let loaded = $state<JellyfinConfig | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let saving = $state(false);
  let testing = $state(false);
  let result = $state<TestResult | null>(null);

  let url = $state('');
  let apiKey = $state('');
  let enabled = $state(false);

  async function load() {
    loading = true;
    try {
      const cfg = await api.jellyfinConfig();
      loaded = cfg;
      url = cfg.url;
      apiKey = cfg.api_key;
      enabled = cfg.enabled;
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  /** Enabling without a server address is the one thing the server rejects. */
  let canSave = $derived(!saving && (!enabled || url.trim() !== ''));

  async function save() {
    saving = true;
    try {
      const cfg = await api.saveJellyfinConfig({
        url: url.trim(),
        api_key: apiKey.trim(),
        enabled,
      });
      loaded = cfg;
      url = cfg.url;
      pushToast('Jellyfin handoff saved.', 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      saving = false;
    }
  }

  async function test() {
    testing = true;
    result = null;
    try {
      // The form's current values, not the saved ones: the point of a test
      // button is to find the typo before it is stored.
      const info = await api.testJellyfin({ url: url.trim(), api_key: apiKey.trim() });
      const name = info.server_name || 'Jellyfin';
      result = { ok: true, message: `Connected to ${name}${info.version ? ` ${info.version}` : ''}` };
    } catch (err) {
      result = { ok: false, message: errorText(err) };
    } finally {
      testing = false;
    }
  }
</script>

<SettingsCard
  title="Jellyfin"
  description="Optional. Caravan already writes Jellyfin's folder layout; turn this on and every import also tells it to rescan.">
  {#snippet action()}
    <!-- The header outlives the body's load branch, so it has to refuse a save
         of values that were never fetched. -->
    <Button variant="primary" size="sm" disabled={loaded === null || !canSave} onclick={save}>
      <Icon name="check" size={14} />
      {saving ? 'Saving…' : 'Save'}
    </Button>
  {/snippet}

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && loaded === null}
    <div class="flex flex-col gap-4">
      <Skeleton class="h-4 w-32" />
      <Skeleton class="h-9 w-full" />
      <Skeleton class="h-9 w-full" />
    </div>
  {:else}
    <Field
      label="Server URL"
      for="jellyfin-url"
      help="Where Jellyfin answers, e.g. http://jellyfin.lan:8096 - the same address you open in a browser.">
      <TextInput id="jellyfin-url" bind:value={url} mono placeholder="http://jellyfin.lan:8096" />
    </Field>

    <Field
      label="API key"
      for="jellyfin-api-key"
      help="Created in Jellyfin under Dashboard - API Keys. Triggering a library scan is an administrator action, so a read-only key will not do.">
      <TextInput id="jellyfin-api-key" bind:value={apiKey} type="password" mono placeholder="•••••" />
    </Field>

    <Toggle
      checked={enabled}
      label="Trigger a Jellyfin scan after every import"
      onchange={(next) => (enabled = next)} />

    <Button
      variant="secondary"
      class="self-start"
      disabled={testing || url.trim() === ''}
      onclick={test}>
      {testing ? 'Testing…' : 'Test connection'}
    </Button>

    {#if result}
      <Banner
        tone={result.ok ? 'success' : 'danger'}
        icon={result.ok ? 'check' : 'warning'}
        message={result.message} />
    {/if}
  {/if}
</SettingsCard>
