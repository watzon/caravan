<script lang="ts">
  /**
   * Settings → Libraries (SPEC §7 `libraries`, PLAN phase 8 task 7).
   *
   * A library is where the multi-instance *arr pattern collapses into one
   * Caravan: Movies and Series each carry their own indexer set, per-pair
   * category overrides, download routing and DLNA visibility, falling back to
   * the global settings wherever they do not answer. So the screen's whole job
   * is to keep "this library decided that" and "the global setting decided
   * that" visibly apart — an override that looks like a default is a setting
   * nobody knows is in effect.
   *
   * Every control saves on change rather than through a per-card Save button.
   * The switcher pills swap the whole screen, so staged edits would either be
   * silently discarded on a switch or have to block it; and both writes answer
   * with the library's whole state, so one response re-renders all four cards.
   * A failed write therefore leaves the screen showing what the server still
   * holds, never an edit that did not land.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import {
    ROUTE_EMBEDDED,
    SETTING_ROUTE_TORRENT,
    SETTING_ROUTE_USENET,
    type DownloadClient,
    type DownloadClientTypeInfo,
    type Library,
    type LibraryIndexer,
    type LibraryKind,
    type LibraryPatch,
    type Protocol,
    type QualityProfile,
    type Settings,
  } from '../api/types';
  import { describeType } from '../downloadClient';
  import { formatCategories, parseCategories } from '../indexer';
  import { pushToast } from '../state/toast.svelte';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import EmptyState from './EmptyState.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';
  import LoadError from './LoadError.svelte';
  import Modal from './Modal.svelte';
  import SettingsCard from './SettingsCard.svelte';
  import Skeleton from './Skeleton.svelte';
  import TextInput from './TextInput.svelte';
  import Toggle from './Toggle.svelte';

  const SELECT_CLASS =
    'h-9 w-full rounded-sm border bg-raised px-3 text-md text-ink ' +
    'focus:border-accent focus:outline-none disabled:opacity-50';

  let libraries = $state<Library[]>([]);
  /** The global settings the routing selects fall back to. */
  let settings = $state<Settings>({});
  let profiles = $state<QualityProfile[]>([]);
  let clients = $state<DownloadClient[]>([]);
  let types = $state<DownloadClientTypeInfo[]>([]);

  let loading = $state(true);
  let error = $state<string | null>(null);
  /** True while a write is in flight, so a second one cannot race it. */
  let busy = $state(false);
  let kind = $state<LibraryKind>('movie');

  /** The (library, indexer) pair whose categories the modal is editing. */
  let editing = $state<LibraryIndexer | null>(null);
  let categoryText = $state('');

  async function load() {
    loading = true;
    try {
      const [loadedLibraries, loadedSettings, loadedProfiles, loadedClients, loadedTypes] =
        await Promise.all([
          api.listLibraries(),
          api.getSettings(),
          api.listQualityProfiles(),
          api.listDownloadClients(),
          api.downloadClientTypes(),
        ]);
      libraries = loadedLibraries;
      settings = loadedSettings;
      profiles = loadedProfiles;
      clients = loadedClients;
      types = loadedTypes;
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  let selected = $derived(libraries.find((l) => l.kind === kind) ?? libraries[0] ?? null);

  /**
   * The selects are mirrored into local state rather than driven straight off
   * `selected`: a rejected write leaves the library object untouched, so
   * nothing would tell Svelte to put the control back to the stored value.
   * `reseed` is what does that, and the effect covers switching libraries.
   */
  let routeTorrent = $state('');
  let routeUsenet = $state('');
  let profileID = $state('');

  function reseed(lib: Library) {
    routeTorrent = lib.route_torrent;
    routeUsenet = lib.route_usenet;
    profileID = lib.quality_profile_id === 0 ? '' : String(lib.quality_profile_id);
  }

  $effect(() => {
    if (selected) reseed(selected);
  });

  /**
   * Clients eligible for a protocol: enabled, and of a type that carries it. A
   * disabled client is not offered for the same reason Settings → Downloads
   * does not offer it — picking it leaves the protocol unrouted without saying
   * so.
   */
  function eligible(protocol: Protocol): DownloadClient[] {
    return clients.filter(
      (client) => client.enabled && describeType(types, client.type).protocol === protocol,
    );
  }

  let torrentClients = $derived(eligible('torrent'));
  let usenetClients = $derived(eligible('usenet'));

  /**
   * What a stored route value is called. Naming searches every client, not just
   * the eligible ones: a route pointing at a client that was since disabled is
   * still that client, and reading it as "built-in engine" would hide a broken
   * setting.
   */
  function routeLabel(value: string): string {
    if (value === '' || value === ROUTE_EMBEDDED) return 'Built-in engine';
    return clients.find((client) => String(client.id) === value)?.name ?? 'Unknown client';
  }

  let globalTorrent = $derived(routeLabel(settings[SETTING_ROUTE_TORRENT] ?? ''));
  let globalUsenet = $derived(routeLabel(settings[SETTING_ROUTE_USENET] ?? ''));

  function profileName(id: number): string {
    return profiles.find((profile) => profile.id === id)?.name ?? `profile ${id}`;
  }

  /**
   * The three select handlers take the picked value rather than reading the
   * bound state back, so nothing depends on whether Svelte's binding listener
   * or this handler runs first.
   */
  function saveProfile(value: string) {
    profileID = value;
    void patch(
      { quality_profile_id: value === '' ? 0 : Number(value) },
      value === ''
        ? `${selected?.name} follows the global default profile.`
        : `${selected?.name} defaults to ${profileName(Number(value))}.`,
    );
  }

  function saveRoute(protocol: Protocol, value: string) {
    const name = selected?.name ?? '';
    if (protocol === 'torrent') {
      routeTorrent = value;
      void patch(
        { route_torrent: value },
        value === ''
          ? `${name} follows the global torrent route.`
          : `${name} routes torrents to ${routeLabel(value)}.`,
      );
      return;
    }
    routeUsenet = value;
    void patch(
      { route_usenet: value },
      value === ''
        ? `${name} follows the global usenet route.`
        : `${name} routes usenet to ${routeLabel(value)}.`,
    );
  }

  async function patch(body: LibraryPatch, note: string) {
    const lib = selected;
    if (!lib) return;
    busy = true;
    try {
      replace(await api.updateLibrary(lib.id, body));
      pushToast(note, 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
      reseed(lib);
    } finally {
      busy = false;
    }
  }

  /**
   * Write one (library, indexer) override. The whole row is rewritten every
   * time, so a caller changing the toggle has to carry the category override
   * with it — the server has no partial verb here, and dropping it would
   * silently widen the search.
   */
  async function writeIndexer(
    row: LibraryIndexer,
    enabled: boolean,
    categories: number[] | null,
    note: string,
  ): Promise<boolean> {
    const lib = selected;
    if (!lib) return false;
    busy = true;
    try {
      replace(await api.setLibraryIndexer(lib.id, row.indexer_id, { enabled, categories }));
      pushToast(note, 'success');
      return true;
    } catch (err) {
      pushToast(errorText(err), 'danger');
      return false;
    } finally {
      busy = false;
    }
  }

  function replace(updated: Library) {
    libraries = libraries.map((l) => (l.id === updated.id ? updated : l));
  }

  function toggleIndexer(lib: Library, row: LibraryIndexer, next: boolean) {
    void writeIndexer(
      row,
      next,
      row.categories_overridden ? row.categories : null,
      next
        ? `${lib.name} searches ${row.name} again.`
        : `${lib.name} no longer searches ${row.name}.`,
    );
  }

  function openCategories(row: LibraryIndexer) {
    editing = row;
    categoryText = formatCategories(row.categories);
  }

  async function saveCategories() {
    const row = editing;
    if (!row) return;
    const ok = await writeIndexer(
      row,
      row.enabled,
      parseCategories(categoryText),
      `Categories saved for ${row.name}.`,
    );
    if (ok) editing = null;
  }

  async function clearCategories() {
    const row = editing;
    if (!row) return;
    const ok = await writeIndexer(row, row.enabled, null, `${row.name} uses its own categories.`);
    if (ok) editing = null;
  }
</script>

{#if error}
  <LoadError message={error} onretry={load} />
{:else if loading && libraries.length === 0}
  <div class="flex flex-col gap-4">
    <Skeleton class="h-7 w-48 rounded-full" />
    <Skeleton class="h-40 w-full rounded-md" />
    <Skeleton class="h-40 w-full rounded-md" />
  </div>
{:else if selected}
  {@const lib = selected}
  <div class="flex flex-col gap-5">
    <div class="flex flex-wrap items-center gap-2" role="group" aria-label="Library">
      {#each libraries as option (option.id)}
        {@const active = option.id === lib.id}
        <button
          type="button"
          aria-pressed={active}
          onclick={() => (kind = option.kind)}
          class="inline-flex h-7 items-center gap-2 rounded-full border px-3 text-sm transition-colors duration-150 ease-out
                 {active
            ? 'border-accent bg-accent-tint text-accent-text'
            : 'border-border bg-surface text-ink-secondary hover:bg-raised hover:text-ink'}">
          <!-- The Adult pill is here for the ordinary reason every pill is:
               GET /libraries returned a row. The server omits that row while
               the module is off, so this list needs no adult rule of its own. -->
          <Icon
            name={option.kind === 'movie' ? 'film' : option.kind === 'adult' ? 'flame' : 'tv'}
            size={14} />
          <span>{option.name}</span>
        </button>
      {/each}
    </div>

    <SettingsCard
      title={lib.name}
      description="Where this library lives, and what it grabs when an item names no profile of its own.">
      <Field
        label="Root path"
        for="library-root"
        help="Relative to the storage root. Moving the whole library is Settings → Storage's job, so it is read-only here.">
        <TextInput id="library-root" value={lib.root_path} readonly mono />
      </Field>

      <Field
        label="Default quality profile"
        for="library-profile"
        help="Used for items in this library that name no profile. An item's own profile always wins.">
        <select
          id="library-profile"
          bind:value={profileID}
          disabled={busy}
          onchange={(event) => saveProfile(event.currentTarget.value)}
          class="{SELECT_CLASS} {profileID === '' ? 'border-border-strong' : 'border-accent'}">
          <option value="">Global default</option>
          {#each profiles as profile (profile.id)}
            <option value={String(profile.id)}>{profile.name}</option>
          {/each}
        </select>
      </Field>

      <p class="text-sm text-ink-secondary">
        <a href="/settings/quality-profiles" class="text-accent-text hover:underline"
          >Manage download profiles</a
        >
        to change the choices available here.
      </p>
    </SettingsCard>

    <SettingsCard
      title="Indexers"
      description="Which sources this library searches, and with which categories. A library can narrow an enabled indexer, but it cannot turn one back on.">
      {#if lib.indexers.length === 0}
        <EmptyState
          icon="link"
          title="No indexers yet"
          message="Add a Torznab or Newznab source before assigning it to a library.">
          {#snippet action()}
            <Button variant="secondary" href="/settings/indexers">Manage indexers</Button>
          {/snippet}
        </EmptyState>
      {:else}
        <ul class="flex flex-col gap-2">
          {#each lib.indexers as row (row.indexer_id)}
            <li
              class="flex flex-wrap items-center gap-3 rounded-md border bg-surface px-3 py-3
                     {row.categories_overridden ? 'border-accent' : 'border-border'}">
              <span
                class="size-2 shrink-0 rounded-full {row.indexer_enabled
                  ? 'bg-success'
                  : 'bg-ink-muted'}">
              </span>
              <span class="sr-only">{row.indexer_enabled ? 'Enabled' : 'Disabled'}</span>

              <div class="flex min-w-0 flex-1 flex-col gap-1">
                <p class="flex flex-wrap items-center gap-2">
                  <span class="truncate text-base font-medium text-ink">{row.name}</span>
                  <Badge mono tone={row.type === 'torznab' ? 'accent' : 'info'}>{row.type}</Badge>
                </p>

                <div class="flex flex-wrap items-center gap-1.5">
                  {#if row.categories.length === 0}
                    <Badge mono tone="warning">every category</Badge>
                  {:else}
                    {#each row.categories as category (category)}
                      <Badge mono tone={row.categories_overridden ? 'accent' : 'neutral'}>
                        {category}
                      </Badge>
                    {/each}
                  {/if}
                  <Button variant="ghost" size="sm" onclick={() => openCategories(row)}>
                    Edit
                    <span class="sr-only">categories for {row.name}</span>
                  </Button>
                </div>

                <!-- Off-everywhere and off-here are different problems, and a row
                     that conflates them sends the user to the wrong screen. -->
                {#if !row.indexer_enabled}
                  <p class="text-sm text-ink-muted">
                    This indexer is disabled globally. <a
                      href="/settings/indexers"
                      class="text-accent-text hover:underline">Open Indexers</a
                    >
                    to enable it.
                  </p>
                {:else if !row.enabled}
                  <p class="text-sm text-ink-muted">Not searched for this library.</p>
                {/if}
              </div>

              <Toggle
                checked={row.enabled}
                labelHidden
                label="Search {row.name} for {lib.name}"
                disabled={busy}
                onchange={(next) => toggleIndexer(lib, row, next)} />
            </li>
          {/each}
        </ul>
      {/if}
    </SettingsCard>

    <SettingsCard
      title="Downloads"
      description="Where this library's grabs land. A blank route follows the global default.">
      <p class="text-sm text-ink-secondary">
        <a href="/settings/downloads" class="text-accent-text hover:underline"
          >Configure global download routing</a
        >
        for every library.
      </p>
      <Field
        label="Torrent route"
        for="library-route-torrent"
        help="Torznab results for this library go here.">
        {#snippet note()}
          <span class="micro-label {routeTorrent === '' ? '' : 'text-accent-text'}">
            {routeTorrent === '' ? 'Global default' : 'Override'}
          </span>
        {/snippet}
        <select
          id="library-route-torrent"
          bind:value={routeTorrent}
          disabled={busy}
          onchange={(event) => saveRoute('torrent', event.currentTarget.value)}
          class="{SELECT_CLASS} {routeTorrent === '' ? 'border-border-strong' : 'border-accent'}">
          <option value="">Global default — {globalTorrent}</option>
          <option value={ROUTE_EMBEDDED}>Built-in engine</option>
          {#each torrentClients as client (client.id)}
            <option value={String(client.id)}>{client.name}</option>
          {/each}
        </select>
      </Field>

      <Field
        label="Usenet route"
        for="library-route-usenet"
        help="Newznab results for this library go here. The built-in engine has no override of its own — it is what clearing this falls back to.">
        {#snippet note()}
          <span class="micro-label {routeUsenet === '' ? '' : 'text-accent-text'}">
            {routeUsenet === '' ? 'Global default' : 'Override'}
          </span>
        {/snippet}
        <select
          id="library-route-usenet"
          bind:value={routeUsenet}
          disabled={busy}
          onchange={(event) => saveRoute('usenet', event.currentTarget.value)}
          class="{SELECT_CLASS} {routeUsenet === '' ? 'border-border-strong' : 'border-accent'}">
          <option value="">Global default — {globalUsenet}</option>
          {#each usenetClients as client (client.id)}
            <option value={String(client.id)}>{client.name}</option>
          {/each}
        </select>
      </Field>
    </SettingsCard>

    <SettingsCard title="Reach" description="Where this library shows up outside Caravan.">
      <Toggle
        checked={lib.dlna_visible}
        label="Share over DLNA"
        disabled={busy}
        onchange={(next) =>
          patch(
            { dlna_visible: next },
            next ? `${lib.name} is shared over DLNA.` : `${lib.name} is hidden from DLNA.`,
          )} />
      <p class="text-sm text-ink-secondary">
        Hiding a library drops its container from the DLNA tree; TVs pick the change up on their
        next browse rather than needing a restart. DLNA has no accounts, so anything shared here is
        open to everyone on the network. Configure screen compatibility and other destinations in
        <a href="/settings/playback" class="text-accent-text hover:underline">Playback</a>.
      </p>
    </SettingsCard>
  </div>
{/if}

{#if editing}
  {@const row = editing}
  <Modal title="Categories — {row.name}" width="max-w-lg" onclose={() => (editing = null)}>
    <div class="flex flex-col gap-4 p-4">
      <Field
        label="Categories"
        for="library-indexer-categories"
        help="Only these ids are sent when this library searches {row.name}. Empty searches every category the indexer has.">
        <TextInput id="library-indexer-categories" bind:value={categoryText} mono placeholder="2000, 5000" />
      </Field>
      <p class="text-sm text-ink-secondary">
        The indexer's own list is
        <span class="font-mono text-ink">
          {row.default_categories.length === 0
            ? 'every category'
            : formatCategories(row.default_categories)}
        </span>.
      </p>
    </div>

    {#snippet footer()}
      {#if row.categories_overridden}
        <Button variant="ghost" disabled={busy} onclick={clearCategories}>Use the indexer's</Button>
        <span class="mx-1 h-5 w-px shrink-0 bg-border"></span>
      {/if}
      <Button variant="ghost" disabled={busy} onclick={() => (editing = null)}>Cancel</Button>
      <Button variant="primary" disabled={busy} onclick={saveCategories}>
        <Icon name="check" size={14} />
        Save
      </Button>
    {/snippet}
  </Modal>
{/if}
