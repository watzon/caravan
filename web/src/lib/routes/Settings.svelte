<script lang="ts">
  /** Settings overview and the route-compatible configuration panes. */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import { SETTING_TMDB_API_KEY, SETTING_TMDB_API_KEY_SET, type Settings } from '../api/types';
  import AdultSettings from '../components/AdultSettings.svelte';
  import Badge from '../components/Badge.svelte';
  import Banner from '../components/Banner.svelte';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import Icon from '../components/Icon.svelte';
  import DownloadsSettings from '../components/DownloadsSettings.svelte';
  import IndexerSettings from '../components/IndexerSettings.svelte';
  import InterfaceSettings from '../components/InterfaceSettings.svelte';
  import LibrariesSettings from '../components/LibrariesSettings.svelte';
  import LoadError from '../components/LoadError.svelte';
  import NotificationSettings from '../components/NotificationSettings.svelte';
  import PlaybackSettings from '../components/PlaybackSettings.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import TextInput from '../components/TextInput.svelte';
  import QualityProfiles from '../components/QualityProfiles.svelte';
  import SecuritySettings from '../components/SecuritySettings.svelte';
  import SettingsCard from '../components/SettingsCard.svelte';
  import RuntimeDiagnostics from '../components/RuntimeDiagnostics.svelte';
  import StorageSettings from '../components/StorageSettings.svelte';
  import TasksSettings from '../components/TasksSettings.svelte';
  import UsersSettings from '../components/UsersSettings.svelte';
  import { UNKNOWN } from '../format';
  import {
    SETTINGS_CATALOG,
    SETTINGS_CATEGORIES,
    settingsEntryForSection,
    settingsHref,
    settingsMatches,
    type SettingsCatalogEntry,
  } from '../settings/catalog';
  import { pushToast } from '../state/toast.svelte';
  import { page } from '../state/page.svelte';
  import { providers } from '../state/providers.svelte';
  import { system } from '../state/system.svelte';

  interface Props {
    /** The /settings/:section route param; '' means the overview. */
    section?: string;
  }

  let { section = '' }: Props = $props();
  const GROUPS = SETTINGS_CATEGORIES.map((category) => ({
    category,
    items: SETTINGS_CATALOG.filter((entry) => entry.category === category),
  }));
  const SHOW_ADVANCED_KEY = 'caravan.settings.show-advanced';
  const SETTINGS_SEARCH_SHORTCUT =
    typeof navigator !== 'undefined' && /mac|iphone|ipad/i.test(navigator.platform || navigator.userAgent)
      ? '⌘K'
      : 'Ctrl K';
  let isOverview = $derived(section === '');
  let activeEntry = $derived<SettingsCatalogEntry | null>(
    isOverview ? null : settingsEntryForSection(section),
  );
  let headerEntry = $derived(activeEntry ?? SETTINGS_CATALOG[0]);
  let query = $state('');

  let settings = $state<Settings | null>(null);
  let showAdvanced = $state(false);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let saving = $state(false);
  let overviewState = $state({ libraries: null as number | null, sources: null as number | null });

  let tmdbKey = $state('');
  let tmdbTest = $state<{ ok: boolean; message: string } | null>(null);
  let testingTMDB = $state(false);

  let status = $derived(system.status);
  let metadataState = $derived(system.metadataCredential);
  let hasTMDBKey = $derived(settings?.[SETTING_TMDB_API_KEY_SET] === 'true');
  let metadataConfigured = $derived(
    hasTMDBKey || (status !== null && metadataState === 'ok'),
  );
  /**
   * Stash-box is configured with the rest of the adult module; this page only
   * points there, and only when the server lists the provider at all — the
   * endpoint omits it when the module is absent, so the card obeys the same
   * promise-of-absence as every other adult surface.
   */
  let hasStashbox = $derived(providers.all.some((p) => p.id === 'stashbox'));
  let tmdbBadge = $derived(
    metadataState === 'invalid'
      ? { tone: 'danger' as const, label: 'Key rejected' }
      : metadataState === 'absent'
        ? { tone: 'warning' as const, label: 'No key' }
        : { tone: 'success' as const, label: 'Connected' },
  );
  let storageConfigured = $derived(Boolean(settings?.storage_root || status?.storage_root));
  let results = $derived(SETTINGS_CATALOG.filter((entry) => settingsMatches(entry, query)));
  let setup = $derived([
    {
      label: 'Choose a storage location',
      description: 'Set the root that will hold the library and downloads.',
      href: '/settings/storage#storage',
      complete: storageConfigured,
    },
    {
      label: 'Connect metadata',
      description: 'Add a TMDB API key for Discover, requests, and TMDB libraries.',
      href: '/settings/metadata#metadata',
      complete: metadataConfigured,
    },
    {
      label: 'Create a library',
      description: 'Add a movie or series library before importing media.',
      href: '/settings/libraries#libraries',
      complete: overviewState.libraries === null ? null : overviewState.libraries > 0,
    },
    {
      label: 'Add a search or download source',
      description: 'Configure an indexer, Usenet server, or download client.',
      href: '/settings/indexers#indexers',
      complete: overviewState.sources === null ? null : overviewState.sources > 0,
    },
  ]);

  async function load() {
    loading = true;
    settings = null;
    error = null;
    try {
      const loaded = await api.getSettings();
      settings = loaded;
      tmdbKey = '';
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  async function loadOverviewState(request: number) {
    const [libraries, indexers, servers, clients] = await Promise.allSettled([
      api.listLibraries(),
      api.listIndexers(),
      api.listUsenetServers(),
      api.listDownloadClients(),
    ]);

    if (request !== overviewRequest || !isOverview) return;
    if (libraries.status === 'fulfilled') overviewState.libraries = libraries.value.length;
    if (indexers.status === 'fulfilled' && servers.status === 'fulfilled' && clients.status === 'fulfilled') {
      overviewState.sources = indexers.value.length + servers.value.length + clients.value.length;
    }
  }

  function toggleAdvanced() {
    showAdvanced = !showAdvanced;
    localStorage.setItem(SHOW_ADVANCED_KEY, String(showAdvanced));
  }

  onMount(() => {
    showAdvanced = localStorage.getItem(SHOW_ADVANCED_KEY) === 'true';
    void load();
  });

  let overviewRequest = 0;
  $effect(() => {
    const request = ++overviewRequest;
    overviewState = { libraries: null, sources: null };
    if (isOverview) void loadOverviewState(request);
  });

  $effect(() => {
    page.actions = settingsSearch;
    return () => (page.actions = null);
  });

  $effect(() => {
    if (activeEntry?.route === '/settings/metadata') void providers.load();
  });

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

  async function testMetadata() {
    testingTMDB = true;
    try {
      await api.testMetadataKey(tmdbKey.trim());
      tmdbTest = { ok: true, message: 'TMDB accepted this key.' };
    } catch (err) {
      tmdbTest = { ok: false, message: errorText(err) };
    } finally {
      testingTMDB = false;
      await system.refresh();
    }
  }
</script>

<div data-settings-layout data-settings-main class="flex min-w-0 flex-1 flex-col gap-5">
  {#if !isOverview}
    <header class="flex flex-col gap-4 border-b border-border pb-4">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="flex min-w-0 flex-1 flex-col gap-1">
          <h1 id={headerEntry.anchor} class="text-lg font-semibold text-ink">
            {headerEntry.label}
          </h1>
          <p class="text-sm text-ink-secondary">{headerEntry.description}</p>
        </div>
        {#if activeEntry?.advanced}
          <Button variant="secondary" size="sm" onclick={toggleAdvanced}>
            {showAdvanced ? 'Hide advanced' : 'Show advanced'}
          </Button>
        {/if}
      </div>
    </header>
  {/if}

  <div class:settings-advanced-hidden={!showAdvanced} class="flex min-w-0 flex-1 flex-col gap-5 {headerEntry.narrow ? 'max-w-3xl' : ''}">
    {#if error}
      <LoadError message={error} onretry={load} />
    {:else if loading && settings === null}
      <div class="flex flex-col gap-4">
        <Skeleton class="h-4 w-32" />
        <Skeleton class="h-9 w-full" />
        <Skeleton class="h-8 w-24" />
      </div>
    {:else if settings === null}
      <LoadError message="Settings could not be loaded." onretry={load} />
    {:else if isOverview}

      <section aria-labelledby="setup-heading" class="flex flex-col gap-3">
        <div>
          <h2 id="setup-heading" class="text-base font-medium text-ink">Set up Caravan</h2>
          <p class="mt-1 text-sm text-ink-secondary">Complete these in order. Each status comes from Caravan’s current state.</p>
        </div>
        <ol class="flex flex-col gap-2">
          {#each setup as item}
            <li>
              <a
                href={item.href}
                class="flex items-center justify-between gap-4 rounded-md bg-raised px-3 py-2
                       transition-colors duration-150 ease-out hover:bg-overlay">
                <span class="min-w-0">
                  <span class="block text-sm font-medium text-ink">{item.label}</span>
                  <span class="block text-sm text-ink-secondary">{item.description}</span>
                </span>
                <Badge tone={item.complete ? 'success' : item.complete === null ? 'neutral' : 'warning'}>
                  {item.complete ? 'Done' : item.complete === null ? 'Checking' : 'Needs setup'}
                </Badge>
              </a>
            </li>
          {/each}
        </ol>
      </section>

      <section aria-labelledby="settings-categories-heading" class="flex flex-col gap-3">
        <div>
          <h2 id="settings-categories-heading" class="text-base font-medium text-ink">Browse settings</h2>
          <p class="mt-1 text-sm text-ink-secondary">Open a page by the job it handles.</p>
        </div>
        <div class="grid gap-4 lg:grid-cols-2">
          {#each GROUPS as group (group.category)}
            <SettingsCard title={group.category}>
              <ul class="flex flex-col gap-1">
                {#each group.items as item (item.route)}
                  <li>
                    <a href={settingsHref(item)} class="flex flex-col rounded-md px-2 py-1.5 hover:bg-raised">
                      <span class="flex items-center gap-2 text-sm font-medium text-ink">
                        {item.label}
                        {#if item.advanced}<Badge tone="neutral">Advanced</Badge>{/if}
                      </span>
                      <span class="text-sm text-ink-secondary">{item.description}</span>
                    </a>
                  </li>
                {/each}
              </ul>
            </SettingsCard>
          {/each}
        </div>
      </section>
    {:else if activeEntry?.route === '/settings/libraries'}
      <LibrariesSettings />
    {:else if activeEntry?.route === '/settings/indexers'}
      <IndexerSettings />
    {:else if activeEntry?.route === '/settings/storage' && settings}
      <StorageSettings {settings} {saving} onsave={save} />
    {:else if activeEntry?.route === '/settings/quality-profiles'}
      <QualityProfiles />
    {:else if activeEntry?.route === '/settings/users'}
      <UsersSettings />
    {:else if activeEntry?.route === '/settings/tasks'}
      <TasksSettings />
    {:else if activeEntry?.route === '/settings/notifications'}
      <NotificationSettings />
    {:else if activeEntry?.route === '/settings/downloads' && settings}
      <DownloadsSettings {settings} {saving} {showAdvanced} onsave={save} />
    {:else if activeEntry?.route === '/settings/playback' && settings}
      <PlaybackSettings {settings} {saving} onsave={save} />
    {:else if activeEntry?.route === '/settings/adult' && settings}
      <AdultSettings {settings} {saving} onsave={save} />
    {:else if activeEntry?.route === '/settings/interface'}
      <InterfaceSettings />
    {:else if activeEntry?.route === '/settings/security' && settings}
      <SecuritySettings {settings} />

      <section id="about">
        <SettingsCard title="About" description="This Caravan.">
          <dl class="grid grid-cols-2 gap-4 sm:grid-cols-4">
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
        </SettingsCard>
      {#if status?.runtime}
        <RuntimeDiagnostics diagnostics={status.runtime} />
      {/if}

      </section>
    {:else if activeEntry?.route === '/settings/metadata'}
      <section class="flex flex-col gap-6">
        <p class="text-sm text-ink-secondary">
          Each provider keeps its own configuration here. Which providers a library uses — and in
          which order — is set per library in
          <a href="/settings/libraries#libraries" class="text-accent-text hover:underline">Libraries</a>.
        </p>

        <SettingsCard
          title="TMDB"
          description="Movies and series: titles, artwork, episode data, Discover, and requests.">
          {#snippet action()}
            <Badge tone={tmdbBadge.tone}>{tmdbBadge.label}</Badge>
          {/snippet}
          {#if metadataState !== 'ok'}
            <Banner
              tone="warning"
              icon="warning"
              title={metadataState === 'invalid' ? 'TMDB rejected this key' : 'No TMDB API key yet'}
              message={metadataState === 'invalid'
                ? system.metadataCredentialReason ||
                  'The key on file was refused. Correct it below and press Test.'
                : 'Discover, requests, and every library that uses TMDB read this key. Enter it below and press Test.'} />
          {/if}

          <Field
            label="TMDB API key"
            for="tmdb-key"
            help="Stored in the database, never in caravan.yaml or logs."
            error={tmdbTest && !tmdbTest.ok ? tmdbTest.message : null}>
            <TextInput
              id="tmdb-key"
              bind:value={tmdbKey}
              type="password"
              mono
              placeholder="•••••"
              oninput={() => (tmdbTest = null)} />
          </Field>
          {#if hasTMDBKey}
            <p class="-mt-2 text-sm text-ink-secondary">A key is stored. Leave blank to keep it.</p>
          {/if}

          {#if tmdbTest?.ok}
            <p class="-mt-2 text-sm text-success">✓ {tmdbTest.message}</p>
          {/if}

          <div class="flex flex-wrap items-center gap-2">
            <Button
              variant="primary"
              disabled={saving || tmdbKey.trim() === ''}
              onclick={() => save({ [SETTING_TMDB_API_KEY]: tmdbKey.trim() }, 'TMDB API key saved.')}>
              <Icon name="check" size={14} />
              {saving ? 'Saving…' : 'Save'}
            </Button>
            <Button
              variant="secondary"
              disabled={!hasTMDBKey || saving}
              onclick={() => save({ [SETTING_TMDB_API_KEY]: '' }, 'TMDB API key cleared.')}>
              Clear
            </Button>
            <Button variant="secondary" disabled={testingTMDB} onclick={testMetadata}>
              {testingTMDB ? 'Testing…' : 'Test'}
            </Button>
          </div>
        </SettingsCard>

        <SettingsCard
          title="AniList"
          description="Anime series: titles, episode counts, and artwork from AniList.">
          {#snippet action()}
            <Badge tone="success">Ready</Badge>
          {/snippet}
          <p class="text-sm text-ink-secondary">
            AniList needs no key or account. To use it, add it to a series library’s provider list
            in
            <a href="/settings/libraries#libraries" class="text-accent-text hover:underline">Libraries</a>.
          </p>
        </SettingsCard>

        <SettingsCard
          title="TVmaze"
          description="Series: titles, real season and episode data, and artwork from TVmaze.">
          {#snippet action()}
            <Badge tone="success">Ready</Badge>
          {/snippet}
          <p class="text-sm text-ink-secondary">
            TVmaze needs no key or account. To use it, add it to a series library’s provider list
            in
            <a href="/settings/libraries#libraries" class="text-accent-text hover:underline">Libraries</a>.
          </p>
        </SettingsCard>

        {#if hasStashbox}
          <SettingsCard title="Stash-box" description="Adult metadata.">
            <p class="text-sm text-ink-secondary">
              The Stash-box endpoint is configured with the rest of the adult module in
              <a href="/settings/adult#adult-content" class="text-accent-text hover:underline">Adult content</a>.
            </p>
          </SettingsCard>
        {/if}
      </section>
    {/if}
  </div>
</div>

{#snippet settingsSearch()}
  <div data-settings-top-search class="relative w-24 lg:w-72">
    <Icon
      name="search"
      size={14}
      class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-ink-muted" />
    <input
      id="settings-search"
      type="search"
      value={query}
      aria-label="Search settings"
      aria-controls={query.trim() ? 'settings-search-results' : undefined}
      placeholder="Search settings"
      oninput={(event) => (query = event.currentTarget.value)}
      onkeydown={(event) => {
        if (event.key !== 'Escape') return;
        query = '';
        event.currentTarget.blur();
      }}
      class="h-8 w-full rounded-md border border-border-strong bg-raised pl-9 pr-3 text-sm text-ink sm:pr-14
             placeholder:text-ink-muted transition-colors duration-150 ease-out
             focus:border-accent focus:outline-none" />
    <kbd
      class="pointer-events-none absolute right-2 top-1/2 hidden -translate-y-1/2 rounded-sm
             bg-surface px-1.5 py-0.5 font-mono text-xs text-ink-muted sm:block">
      {SETTINGS_SEARCH_SHORTCUT}
    </kbd>

    {#if query.trim()}
      <div
        id="settings-search-results"
        aria-live="polite"
        class="absolute right-0 top-full z-50 mt-2 max-h-80 w-[min(24rem,calc(100vw-2rem))]
               overflow-y-auto rounded-md border border-border-strong bg-surface shadow-xl">
        <p class="border-b border-border px-3 py-2 text-xs text-ink-secondary">
          {results.length === 1 ? '1 matching setting' : `${results.length} matching settings`}
        </p>
        {#if results.length}
          <ul class="flex flex-col gap-1 p-1">
            {#each results as item (item.route)}
              <li>
                <a
                  href={settingsHref(item)}
                  class="flex flex-col rounded-sm px-3 py-2 hover:bg-raised"
                  onclick={() => (query = '')}>
                  <span class="text-sm font-medium text-ink">{item.label}</span>
                  <span class="text-xs text-ink-secondary">{item.description}</span>
                </a>
              </li>
            {/each}
          </ul>
        {:else}
          <p class="px-3 py-3 text-sm text-ink-secondary">No settings match that search.</p>
        {/if}
      </div>
    {/if}
  </div>
{/snippet}

<style>
  .settings-advanced-hidden :global([data-settings-advanced]) {
    display: none;
  }
</style>
