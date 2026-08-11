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
    settingsCategoryLabel,
    settingsDescription,
    settingsEntryForSection,
    settingsHref,
    settingsLabel,
    settingsMatches,
    type SettingsCatalogEntry,
  } from '../settings/catalog';
  import { pushToast } from '../state/toast.svelte';
  import { page } from '../state/page.svelte';
  import { session } from '../state/session.svelte';
  import { system } from '../state/system.svelte';
  import { useI18n } from '../i18n.svelte';

  const { t, tp } = useI18n();

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
  let results = $derived(SETTINGS_CATALOG.filter((entry) => settingsMatches(entry, query, t)));
  let setup = $derived([
    {
      label: t('route.settings.storageSetupLabel'),
      description: t('route.settings.storageSetupDescription'),
      href: '/settings/storage#storage',
      complete: storageConfigured,
    },
    {
      label: t('route.settings.metadataSetupLabel'),
      description: t('route.settings.metadataSetupDescription'),
      href: '/settings/metadata#metadata',
      complete: metadataConfigured,
    },
    {
      label: t('route.settings.librarySetupLabel'),
      description: t('route.settings.librarySetupDescription'),
      href: '/settings/libraries#libraries',
      complete: overviewState.libraries === null ? null : overviewState.libraries > 0,
    },
    {
      label: t('route.settings.sourceSetupLabel'),
      description: t('route.settings.sourceSetupDescription'),
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
            {settingsLabel(headerEntry, t)}
          </h1>
          <p class="text-sm text-ink-secondary">{settingsDescription(headerEntry, t)}</p>
        </div>
        {#if activeEntry?.advanced}
          <Button variant="secondary" size="sm" onclick={toggleAdvanced}>
            {showAdvanced ? t('route.settings.hideAdvanced') : t('route.settings.showAdvanced')}
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
      <LoadError message={t('route.settings.loadError')} onretry={load} />
    {:else if isOverview}

      <section aria-labelledby="setup-heading" class="flex flex-col gap-3">
        <div>
          <h2 id="setup-heading" class="text-base font-medium text-ink">{t('route.settings.setupTitle')}</h2>
          <p class="mt-1 text-sm text-ink-secondary">{t('route.settings.setupDescription')}</p>
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
                  {item.complete ? t('route.settings.done') : item.complete === null ? t('route.settings.checking') : t('route.settings.needsSetup')}
                </Badge>
              </a>
            </li>
          {/each}
        </ol>
      </section>

      <section aria-labelledby="settings-categories-heading" class="flex flex-col gap-3">
        <div>
          <h2 id="settings-categories-heading" class="text-base font-medium text-ink">{t('route.settings.browseTitle')}</h2>
          <p class="mt-1 text-sm text-ink-secondary">{t('route.settings.browseDescription')}</p>
        </div>
        <div class="grid gap-4 lg:grid-cols-2">
          {#each GROUPS as group (group.category)}
            <SettingsCard title={settingsCategoryLabel(group.category, t)}>
              <ul class="flex flex-col gap-1">
                {#each group.items as item (item.route)}
                  <li>
                    <a href={settingsHref(item)} class="flex flex-col rounded-md px-2 py-1.5 hover:bg-raised">
                      <span class="flex items-center gap-2 text-sm font-medium text-ink">
                        {settingsLabel(item, t)}
                        {#if item.advanced}<Badge tone="neutral">{t('route.settings.advanced')}</Badge>{/if}
                      </span>
                      <span class="text-sm text-ink-secondary">{settingsDescription(item, t)}</span>
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
    {:else if activeEntry?.route === '/settings/interface'}
      <InterfaceSettings />
    {:else if activeEntry?.route === '/settings/security' && settings}
      <SecuritySettings {settings} />

      <section id="about">
        <SettingsCard title={t('route.settings.about')} description={t('route.settings.aboutDescription')}>
          <dl class="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <div>
              <dt class="micro-label">{t('route.settings.version')}</dt>
              <dd class="mt-1 font-mono text-sm text-ink">{status?.version || UNKNOWN}</dd>
            </div>
            <div>
              <dt class="micro-label">{t('route.settings.mode')}</dt>
              <dd class="mt-1 font-mono text-sm text-ink">{status?.mode || UNKNOWN}</dd>
            </div>
            <div>
              <dt class="micro-label">{t('route.settings.schema')}</dt>
              <dd class="mt-1 font-mono text-sm text-ink">
                {status ? `v${status.schema_version}` : UNKNOWN}
              </dd>
            </div>
            <div>
              <dt class="micro-label">{t('route.settings.libraryFiles')}</dt>
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
          {t('route.settings.metadataDescription')}
          <a href="/settings/libraries#libraries" class="text-accent-text hover:underline">
            {t('route.settings.libraries')}
          </a>.
        </p>

        <ProviderKeyCard
          providerId="tmdb"
          title="TMDB"
          description={t('route.settings.tmdbDescription')}
          inputId="tmdb-key"
          keySetting={SETTING_TMDB_API_KEY}
          keySetSetting={SETTING_TMDB_API_KEY_SET}
          absentMessage={t('route.settings.tmdbAbsent')}
          {settings}
          {saving}
          onsave={save} />

        <ProviderKeyCard
          providerId="thetvdb"
          title="TheTVDB"
          description={t('route.settings.thetvdbDescription')}
          inputId="thetvdb-key"
          keySetting={SETTING_THETVDB_API_KEY}
          keySetSetting={SETTING_THETVDB_API_KEY_SET}
          absentMessage={t('route.settings.thetvdbAbsent')}
          pin={{
            setting: SETTING_THETVDB_PIN,
            inputId: 'thetvdb-pin',
            label: t('route.settings.subscriberPin'),
            help: t('route.settings.subscriberPinHelp'),
          }}
          {settings}
          {saving}
          onsave={save} />

        <SettingsCard title="AniList" description={t('route.settings.anilistDescription')}>
          {#snippet action()}
            <Badge tone="success">{t('route.settings.ready')}</Badge>
          {/snippet}
          <p class="text-sm text-ink-secondary">{t('route.settings.anilistMessage')}</p>
        </SettingsCard>

        <SettingsCard title="TVmaze" description={t('route.settings.tvmazeDescription')}>
          {#snippet action()}
            <Badge tone="success">{t('route.settings.ready')}</Badge>
          {/snippet}
          <p class="text-sm text-ink-secondary">{t('route.settings.tvmazeMessage')}</p>
        </SettingsCard>

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
      aria-label={t('route.settings.searchLabel')}
      aria-controls={query.trim() ? 'settings-search-results' : undefined}
      placeholder={t('route.settings.searchPlaceholder')}
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
          {tp('route.settings.matching', results.length)}
        </p>
        {#if results.length}
          <ul class="flex flex-col gap-1 p-1">
            {#each results as item (item.route)}
              <li>
                <a
                  href={settingsHref(item)}
                  class="flex flex-col rounded-sm px-3 py-2 hover:bg-raised"
                  onclick={() => (query = '')}>
                  <span class="text-sm font-medium text-ink">{settingsLabel(item, t)}</span>
                  <span class="text-xs text-ink-secondary">{settingsDescription(item, t)}</span>
                </a>
              </li>
            {/each}
          </ul>
        {:else}
          <p class="px-3 py-3 text-sm text-ink-secondary">{t('route.settings.noMatch')}</p>
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
