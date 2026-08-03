<script lang="ts">
  /** Settings for general configuration, indexers, engine defaults, profiles and storage. */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import { SETTING_TMDB_API_KEY, type Settings } from '../api/types';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import Icon from '../components/Icon.svelte';
  import DownloadsSettings from '../components/DownloadsSettings.svelte';
  import IndexerSettings from '../components/IndexerSettings.svelte';
  import LibrariesSettings from '../components/LibrariesSettings.svelte';
  import LoadError from '../components/LoadError.svelte';
  import PlaybackSettings from '../components/PlaybackSettings.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import TextInput from '../components/TextInput.svelte';
  import QualityProfiles from '../components/QualityProfiles.svelte';
  import SecuritySettings from '../components/SecuritySettings.svelte';
  import SettingsCard from '../components/SettingsCard.svelte';
  import StorageSettings from '../components/StorageSettings.svelte';
  import UsersSettings from '../components/UsersSettings.svelte';
  import { UNKNOWN } from '../format';
  import { pushToast } from '../state/toast.svelte';
  import { system } from '../state/system.svelte';

  interface Props {
    /** The /settings/:section route param; '' means the bare /settings. */
    section?: string;
  }

  let { section = '' }: Props = $props();

  type SectionKey =
    | 'libraries'
    | 'metadata'
    | 'quality-profiles'
    | 'storage'
    | 'indexers'
    | 'downloads'
    | 'playback'
    | 'users'
    | 'security';

  interface SectionDef {
    key: SectionKey;
    label: string;
    title: string;
    /** One plain-language sentence under the pane title. */
    blurb: string;
    /** Pure-form panes keep a narrow column; card and list panes fill
     * the content column, as the Paper mocks have it. */
    narrow?: boolean;
  }

  /**
   * Nine sections grouped the way the product thinks (the Paper redesign).
   * Downloads and Playback each hold several cards rather than several rail
   * entries: the things inside them are one decision, not four.
   */
  const GROUPS: { label: string; items: SectionDef[] }[] = [
    {
      label: 'Library',
      items: [
        {
          key: 'libraries',
          label: 'Libraries',
          title: 'Libraries',
          blurb:
            'Movies and Series each carry their own indexers, categories, routing and reach. Anything a library does not answer falls back to the global setting.',
        },
        {
          key: 'metadata',
          narrow: true,
          label: 'Metadata',
          title: 'Metadata',
          blurb: 'TMDB supplies titles, artwork and episode data. The key lives in the database, never in logs.',
        },
        {
          key: 'quality-profiles',
          label: 'Quality profiles',
          title: 'Quality profiles',
          blurb: 'What to grab and when to upgrade: the scoring rules search runs releases through.',
        },
        {
          key: 'storage',
          narrow: true,
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
        },
        {
          key: 'downloads',
          label: 'Downloads',
          title: 'Downloads',
          blurb: 'What actually pulls a release down. Both engines are built in; external clients are optional.',
        },
      ],
    },
    {
      label: 'Playback',
      items: [
        {
          key: 'playback',
          label: 'Playback',
          title: 'Playback',
          blurb: 'How the library reaches a screen, and what the screen on the other end can decode.',
        },
      ],
    },
    {
      label: 'System',
      items: [
        {
          key: 'users',
          narrow: true,
          label: 'Users',
          title: 'Users',
          blurb: 'Who can sign in, and what each of them may do. With no accounts, Caravan is open to anyone who can reach it.',
        },
        {
          key: 'security',
          narrow: true,
          label: 'Security',
          title: 'Security',
          blurb: 'Your own password, the API key external tools use, and what this Caravan is running.',
        },
      ],
    },
  ];

  /**
   * The slugs the eleven-section rail used. An old deep link lands on the pane
   * that absorbed it rather than falling through to Metadata.
   */
  const LEGACY_SECTIONS: Record<string, SectionKey> = {
    engine: 'downloads',
    'usenet-servers': 'downloads',
    'download-clients': 'downloads',
    dlna: 'playback',
    jellyfin: 'playback',
    'tv-profile': 'playback',
    general: 'metadata',
  };

  const SECTIONS = new Map<string, SectionDef>(
    GROUPS.flatMap((g) => g.items).map((item) => [item.key, item]),
  );

  /** An unknown or absent section slug lands on Metadata rather than a 404:
   * the pane exists either way, and old /settings links keep working. */
  let tab = $derived<SectionKey>(
    SECTIONS.has(section) ? (section as SectionKey) : (LEGACY_SECTIONS[section] ?? 'metadata'),
  );
  let def = $derived(SECTIONS.get(tab)!);

  /** Quiet rail counts for the list sections, fetched lazily and best-effort:
   * a count that fails to load renders as no count, never as an error. */
  let railCounts = $state<Partial<Record<SectionKey, number>>>({});
  onMount(() => {
    void (async () => {
      try {
        railCounts.indexers = (await api.listIndexers()).length;
      } catch {
        // No count is the honest render for "could not ask".
      }
      // Downloads holds both lists, so it carries one number. A partial sum
      // would misreport, so either both answer or nothing is shown.
      try {
        const [servers, clients] = await Promise.all([
          api.listUsenetServers(),
          api.listDownloadClients(),
        ]);
        railCounts.downloads = servers.length + clients.length;
      } catch {
        // Same reason.
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

  <div class="flex min-w-0 flex-1 flex-col gap-5 {def.narrow ? 'max-w-3xl' : ''}">
    <header class="flex flex-col gap-1 border-b border-border pb-4">
      <h3 class="text-md font-semibold text-ink">{def.title}</h3>
      <p class="text-sm text-ink-secondary">{def.blurb}</p>
    </header>

    <!-- These own their fetches, so they render whether or not /settings loaded. -->
    {#if tab === 'libraries'}
      <LibrariesSettings />
    {:else if tab === 'indexers'}
      <IndexerSettings />
    {:else if tab === 'quality-profiles'}
      <QualityProfiles />
    {:else if tab === 'users'}
      <UsersSettings />
    {:else if error}
      <LoadError message={error} onretry={load} />
    {:else if loading && settings === null}
      <div class="flex flex-col gap-4">
        <Skeleton class="h-4 w-32" />
        <Skeleton class="h-9 w-full" />
        <Skeleton class="h-8 w-24" />
      </div>
    {:else if tab === 'downloads' && settings}
      <DownloadsSettings {settings} {saving} onsave={save} />
    {:else if tab === 'playback' && settings}
      <PlaybackSettings {settings} {saving} onsave={save} />
    {:else if tab === 'security' && settings}
      <SecuritySettings {settings} />

      <!-- About lives here rather than under Metadata: what this Caravan is
           running is a system fact, not a library one. -->
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
      </section>
    {:else if settings}
      <!-- Storage owns two operations with very different consequences, so it
           owns its own component and its own migration polling. -->
      <StorageSettings {settings} />
    {/if}
  </div>
</div>
