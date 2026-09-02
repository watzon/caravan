<script lang="ts">
  import { useI18n } from '../i18n.svelte';
  /**
   * The Stash handoff for the adult library.
   *
   * Deliberately the Jellyfin card again, for the other library: three values
   * edited, validated and tested together, with no scan button — the scan is
   * queued by an adult import and run by the job queue, so a manual one would
   * be a second, weaker path to the same effect.
   *
   * What is not the same is the scope line. Jellyfin is told "rescan"; Stash is
   * told to scan one directory, and a user handing an adult server an API key
   * deserves to read that promise on the card rather than infer it. The
   * component never renders on a browser the module is not visible to —
   * PlaybackSettings owns that gate — but it is also structurally safe if it
   * did: every route it calls is behind the server's own /adult gate.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { StashConfig } from '../api/types';
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

  let loaded = $state<StashConfig | null>(null);
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
      const cfg = await api.stashConfig();
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
      const cfg = await api.saveStashConfig({
        url: url.trim(),
        api_key: apiKey.trim(),
        enabled,
      });
      loaded = cfg;
      url = cfg.url;
      pushToast(t('component.stashSettings.saved'), 'success');
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
      const info = await api.testStash({ url: url.trim(), api_key: apiKey.trim() });
      const version = info.version || t('component.stashSettings.unknownBuild');
      result = { ok: true, message: t('component.stashSettings.connected', { version }) };
    } catch (err) {
      result = { ok: false, message: errorText(err) };
    } finally {
      testing = false;
    }
  }

  const { t, tp } = useI18n();
</script>

<SettingsCard
  title={t('component.stashSettings.stash')}
  description={t('component.stashSettings.theAdultLibrarySJellyfinImportsTriggerAScanScopedToLibraryAdultThenCaravanPushesTheStashBoxIdSoScenesArriveAlreadyIdentified')}>
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
      label={t('component.stashSettings.serverUrl')}
      for="stash-url"
      help={t('component.stashSettings.whereStashAnswersEGHttpStashLan9999TheSameAddressYouOpenInABrowser')}>
      <TextInput id="stash-url" bind:value={url} mono placeholder={t('component.stashSettings.httpStashLan9999')} />
    </Field>

    <Field
      label={t('component.stashSettings.apiKey')}
      for="stash-api-key"
      help={t('component.stashSettings.createdInStashUnderSettingsSecurityRequiredOnlyOnAStashThatHasAuthenticationTurnedOn')}>
      <TextInput id="stash-api-key" bind:value={apiKey} type="password" mono placeholder="•••••" />
    </Field>

    <Toggle
      checked={enabled}
      label={t('component.stashSettings.identifyScenesInStashAfterEveryAdultImport')}
      onchange={(next) => (enabled = next)} />

    <!-- The scope promise, on the card rather than in the docs: this is an API
         key for somebody's adult server, and what Caravan does with it is one
         directory and nothing else. -->
    <p class="text-sm text-ink-secondary">
      {t('component.stashSettings.adultScanScope', { path: 'library/Adult' })}
    </p>

    <Button
      variant="secondary"
      class="self-start"
      disabled={testing || url.trim() === ''}
      onclick={test}>
      {testing ? t('component.stashSettings.testing') : t('component.stashSettings.testConnection')}
    </Button>

    {#if result}
      <Banner
        tone={result.ok ? 'success' : 'danger'}
        icon={result.ok ? 'check' : 'warning'}
        message={result.message} />
    {/if}
  {/if}
</SettingsCard>
