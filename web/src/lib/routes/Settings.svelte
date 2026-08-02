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

  interface Props {
    /** The /settings/:section route param; '' means the bare /settings. */
    section?: string;
  }

  let { section = '' }: Props = $props();

  type SectionKey =
    | 'metadata'
    | 'quality-profiles'
    | 'storage'
    | 'indexers'
    | 'download-clients'
    | 'usenet-servers'
    | 'engine'
    | 'dlna'
    | 'jellyfin'
    | 'tv-profile'
    | 'security';

  interface SectionDef {
    key: SectionKey;
    label: string;
    title: string;
    /** One plain-language sentence under the pane title. */
    blurb: string;
    /** List-heavy panes get the wider column. */
    wide?: boolean;
  }

  /**
   * Eleven sections grouped the way the product thinks (the Paper redesign):
   * a flat tab row stopped scaling at about six.
   */
  const GROUPS: { label: string; items: SectionDef[] }[] = [
    {
      label: 'Library',
      items: [
        {
          key: 'metadata',
          label: 'Metadata',
          title: 'Metadata',
          blurb: 'TMDB supplies titles, artwork and episode data. The key lives in the database, never in logs.',
        },
        {
          key: 'quality-profiles',
          label: 'Quality profiles',
          title: 'Quality profiles',
          blurb: 'What to grab and when to upgrade: the scoring rules search runs releases through.',
          wide: true,
        },
        {
          key: 'storage',
          label: 'Storage',
          title: 'Storage',
          blurb: 'The storage root — where the library, downloads and database live.',
        },
      ],
    },
    {
      label: 'Acquisition',
      items: [
        {
          key: 'indexers',
          label: 'Indexers',
          title: 'Indexers',
          blurb: 'Torznab and Newznab sources. Point Prowlarr here, or add indexers directly.',
          wide: true,
        },
        {
          key: 'download-clients',
          label: 'Download clients',
          title: 'Download clients',
          blurb: 'External torrent and usenet programs. Optional — Caravan downloads on its own without them.',
          wide: true,
        },
        {
          key: 'usenet-servers',
          label: 'Usenet servers',
          title: 'Usenet servers',
          blurb: 'News servers for the built-in Usenet engine. A second server at a higher priority number backs up articles the first is missing.',
          wide: true,
        },
        {
          key: 'engine',
          label: 'Engine',
          title: 'Engine',
          blurb: 'The embedded BitTorrent engine\u2019s defaults. A download can override its own limits from the queue.',
        },
      ],
    },
    {
      label: 'Playback',
      items: [
        {
          key: 'dlna',
          label: 'DLNA',
          title: 'DLNA',
          blurb: 'The built-in media server. TVs and apps on this network can browse and play the library — no account, no transcoding.',
        },
        {
          key: 'jellyfin',
          label: 'Jellyfin',
          title: 'Jellyfin',
          blurb: 'Caravan already writes Jellyfin\u2019s folder layout. Turn this on and every import also tells Jellyfin to rescan.',
        },
        {
          key: 'tv-profile',
          label: 'TV profile',
          title: 'TV profile',
          blurb: 'What the TV on the other end can decode. It warns before you grab \u2014 it never hides a release.',
        },
      ],
    },
    {
      label: 'System',
      items: [
        {
          key: 'security',
          label: 'Security',
          title: 'Security',
          blurb: 'Password and sessions for this Caravan.',
        },
      ],
    },
  ];

  const SECTIONS = new Map<string, SectionDef>(
    GROUPS.flatMap((g) => g.items).map((item) => [item.key, item]),
  );

  /** An unknown or absent section slug lands on Metadata rather than a 404:
   * the pane exists either way, and old /settings links keep working. */
  let tab = $derived<SectionKey>(SECTIONS.has(section) ? (section as SectionKey) : 'metadata');
  let def = $derived(SECTIONS.get(tab)!);

  /** Quiet rail counts for the list sections, fetched lazily and best-effort:
   * a count that fails to load renders as no count, never as an error. */
  let railCounts = $state<Partial<Record<SectionKey, number>>>({});
  onMount(() => {
    void (async () => {
      const fetches: [SectionKey, () => Promise<{ length: number }>][] = [
        ['indexers', () => api.listIndexers()],
        ['download-clients', () => api.listDownloadClients()],
        ['usenet-servers', () => api.listUsenetServers()],
      ];
      for (const [key, fetchList] of fetches) {
        try {
          railCounts[key] = (await fetchList()).length;
        } catch {
          // No count is the honest render for "could not ask".
        }
      }
    })();
  });
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

<div class="flex flex-col gap-6 md:flex-row md:gap-7">
  <!-- The settings rail (Paper redesign): grouped sections in the sidebar's
       own grammar. On narrow screens it wraps into rows above the pane. -->
  <nav
    class="flex flex-row flex-wrap gap-x-6 gap-y-4 md:w-52 md:shrink-0 md:flex-col md:gap-6"
    aria-label="Settings sections">
    {#each GROUPS as group (group.label)}
      <div class="flex flex-col gap-0.5">
        <p class="micro-label px-2.5 pb-1.5">{group.label}</p>
        {#each group.items as item (item.key)}
          {@const active = tab === item.key}
          <a
            href="/settings/{item.key}"
            aria-current={active ? 'page' : undefined}
            class="flex items-center justify-between gap-3 rounded-md border-l-2 py-1.5 pl-2.5 pr-2.5 text-base transition-colors duration-150 ease-out
                   {active
              ? 'border-l-accent bg-accent-tint text-accent-text'
              : 'border-l-transparent text-ink-secondary hover:bg-raised hover:text-ink'}">
            <span>{item.label}</span>
            {#if railCounts[item.key]}
              <span
                class="text-sm font-medium tabular-nums {active
                  ? 'text-accent-text'
                  : 'text-ink-muted'}">
                {railCounts[item.key]}
              </span>
            {/if}
          </a>
        {/each}
      </div>
    {/each}
  </nav>

  <div class="flex min-w-0 flex-1 flex-col gap-5 {def.wide ? 'max-w-5xl' : 'max-w-3xl'}">
    <header class="flex flex-col gap-1 border-b border-border pb-4">
      <h3 class="text-md font-semibold text-ink">{def.title}</h3>
      <p class="text-sm text-ink-secondary">{def.blurb}</p>
    </header>

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
  {:else if tab === 'metadata'}
    <section class="flex flex-col gap-6">
      <Field
        label="TMDB API key"
        for="tmdb-key"
        help="Stored in the database, never in caravan.yaml or logs.">
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
</div>
