<script lang="ts">
  /** Settings overview and the route-compatible configuration panes. */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import {
    SETTING_THETVDB_API_KEY,
    SETTING_THETVDB_API_KEY_SET,
    SETTING_THETVDB_PIN,
    SETTING_TMDB_API_KEY,
    SETTING_TMDB_API_KEY_SET,
    type Settings,
  } from '../api/types';
  import AdultSettings from '../components/AdultSettings.svelte';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import Icon from '../components/Icon.svelte';
  import DownloadsSettings from '../components/DownloadsSettings.svelte';
  import IndexerSettings from '../components/IndexerSettings.svelte';
  import InterfaceSettings from '../components/InterfaceSettings.svelte';
  import LibrariesSettings from '../components/LibrariesSettings.svelte';
  import LoadError from '../components/LoadError.svelte';
  import NotificationSettings from '../components/NotificationSettings.svelte';
  import PlaybackSettings from '../components/PlaybackSettings.svelte';
  import ProviderKeyCard from '../components/ProviderKeyCard.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import QualityProfiles from '../components/QualityProfiles.svelte';
  import SecuritySettings from '../components/SecuritySettings.svelte';
  import SettingsCard from '../components/SettingsCard.svelte';
  import StashboxSettings from '../components/StashboxSettings.svelte';
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
  import { session } from '../state/session.svelte';
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

  let status = $derived(system.status);
  let metadataState = $derived(system.metadataCredential);
  let hasTMDBKey = $derived(settings?.[SETTING_TMDB_API_KEY_SET] === 'true');
  // TMDB-only on purpose, and it stays that way as key-based providers arrive:
  // Discover and the request flow are TMDB surfaces, so a TheTVDB key leaves
  // the checklist item exactly as unfinished as it was.
  let metadataConfigured = $derived(
    hasTMDBKey || (status !== null && metadataState === 'ok'),
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
      settings = await api.getSettings();
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
      <AdultSettings {settings} />
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
    {:else if activeEntry?.route === '/settings/metadata' && settings}
      <section class="flex flex-col gap-6">
        <p class="text-sm text-ink-secondary">
          Each provider keeps its own configuration here. Which providers a library uses — and in
          which order — is set per library in
          <a href="/settings/libraries#libraries" class="text-accent-text hover:underline">Libraries</a>.
        </p>

        <!--
          The key-based providers come first as a pair, then the keyless ones:
          a reader scanning for something to enter finds every field before the
          cards that only say "Ready".
        -->
        <ProviderKeyCard
          providerId="tmdb"
          title="TMDB"
          description="Movies and series: titles, artwork, episode data, Discover, and requests."
          inputId="tmdb-key"
          keySetting={SETTING_TMDB_API_KEY}
          keySetSetting={SETTING_TMDB_API_KEY_SET}
          absentMessage="Discover, requests, and every library that uses TMDB read this key. Enter it below and press Test."
          {settings}
          {saving}
          onsave={save} />

        <ProviderKeyCard
          providerId="thetvdb"
          title="TheTVDB"
          description="Series: titles, episode data, and alternate orders from TheTVDB. Needs your own v4 API key."
          inputId="thetvdb-key"
          keySetting={SETTING_THETVDB_API_KEY}
          keySetSetting={SETTING_THETVDB_API_KEY_SET}
          absentMessage="Every series library that uses TheTVDB reads this key. Enter it below and press Test."
          pin={{
            setting: SETTING_THETVDB_PIN,
            inputId: 'thetvdb-pin',
            label: 'Subscriber PIN',
            help: 'Only for user-supported keys. Leave blank for a licensed key.',
          }}
          {settings}
          {saving}
          onsave={save} />

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

        <!--
          Gated on the session rather than on the provider list: every route the
          card calls 404s while the module is off, so a card that mounted and
          then failed to load would itself be the trace the module promises not
          to leave.
        -->
        {#if session.adult}
          <StashboxSettings />
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
