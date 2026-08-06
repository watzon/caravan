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
    type MetadataProviderInfo,
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

  type AutosaveStatus = 'saving' | 'saved' | 'error';

  let libraries = $state<Library[]>([]);
  /** The global settings the routing selects fall back to. */
  let settings = $state<Settings>({});
  let profiles = $state<QualityProfile[]>([]);
  let clients = $state<DownloadClient[]>([]);
  let types = $state<DownloadClientTypeInfo[]>([]);
  let providers = $state<MetadataProviderInfo[]>([]);

  let loading = $state(true);
  let error = $state<string | null>(null);
  /** True while a write is in flight, so a second one cannot race it. */
  let busy = $state(false);
  /**
   * Selection is by library id, not kind: since several libraries may share a
   * kind, the id is the only thing that names one pill.
   */
  let selectedID = $state<number | null>(null);
  let scanning = $state(false);
  let scanMessage = $state<string | null>(null);
  let autosaveStates = $state<Record<string, AutosaveStatus>>({});

  /** The add-library modal's staged fields; null when closed. */
  let adding = $state<{ kind: LibraryKind; name: string; root: string; provider: string } | null>(
    null,
  );
  /** Set while the delete confirmation is open for the selected library. */
  let confirmingDelete = $state(false);

  /** The (library, indexer) pair whose categories the modal is editing. */
  let editing = $state<LibraryIndexer | null>(null);
  let categoryText = $state('');

  async function load() {
    loading = true;
    try {
      const [
        loadedLibraries,
        loadedSettings,
        loadedProfiles,
        loadedClients,
        loadedTypes,
        loadedProviders,
      ] = await Promise.all([
        api.listLibraries(),
        api.getSettings(),
        api.listQualityProfiles(),
        api.listDownloadClients(),
        api.downloadClientTypes(),
        api.listMetadataProviders(),
      ]);
      libraries = loadedLibraries;
      settings = loadedSettings;
      profiles = loadedProfiles;
      clients = loadedClients;
      types = loadedTypes;
      providers = loadedProviders;
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  let selected = $derived(libraries.find((l) => l.id === selectedID) ?? libraries[0] ?? null);

  /** The kinds the create form may offer: whatever some provider can serve. */
  let creatableKinds = $derived(
    (['movie', 'tv', 'adult'] as LibraryKind[]).filter((k) =>
      providers.some((p) => p.kinds.includes(k)),
    ),
  );

  function providersFor(k: LibraryKind): MetadataProviderInfo[] {
    return providers.filter((p) => p.kinds.includes(k));
  }

  function providerName(id: string): string {
    return providers.find((p) => p.id === id)?.name ?? id;
  }

  /**
   * The selects are mirrored into local state rather than driven straight off
   * `selected`: a rejected write leaves the library object untouched, so
   * nothing would tell Svelte to put the control back to the stored value.
   * `reseed` is what does that, and the effect covers switching libraries.
   */
  let routeTorrent = $state('');
  let routeUsenet = $state('');
  let profileID = $state('');
  let nameDraft = $state('');

  function reseed(lib: Library) {
    routeTorrent = lib.route_torrent;
    routeUsenet = lib.route_usenet;
    profileID = lib.quality_profile_id === 0 ? '' : String(lib.quality_profile_id);
    nameDraft = lib.name;
  }

  function commitName() {
    const lib = selected;
    if (!lib) return;
    const name = nameDraft.trim();
    if (name === '' || name === lib.name) {
      nameDraft = lib.name;
      return;
    }
    void patch({ name }, `Library renamed to ${name}.`, autosaveKey(lib, 'name'));
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

  function autosaveKey(lib: Library, field: string): string {
    return `${lib.id}:${field}`;
  }

  function setAutosaveState(key: string, status: AutosaveStatus) {
    autosaveStates =
      status === 'saving' ? { [key]: status } : { ...autosaveStates, [key]: status };
  }

  /**
   * The three select handlers take the picked value rather than reading the
   * bound state back, so nothing depends on whether Svelte's binding listener
   * or this handler runs first.
   */
  function saveProfile(value: string) {
    const lib = selected;
    if (!lib) return;
    profileID = value;
    void patch(
      { quality_profile_id: value === '' ? 0 : Number(value) },
      value === ''
        ? `${lib.name} follows the global default profile.`
        : `${lib.name} defaults to ${profileName(Number(value))}.`,
      autosaveKey(lib, 'profile'),
    );
  }

  function saveRoute(protocol: Protocol, value: string) {
    const lib = selected;
    if (!lib) return;
    if (protocol === 'torrent') {
      routeTorrent = value;
      void patch(
        { route_torrent: value },
        value === ''
          ? `${lib.name} follows the global torrent route.`
          : `${lib.name} routes torrents to ${routeLabel(value)}.`,
        autosaveKey(lib, 'torrent-route'),
      );
      return;
    }
    routeUsenet = value;
    void patch(
      { route_usenet: value },
      value === ''
        ? `${lib.name} follows the global usenet route.`
        : `${lib.name} routes usenet to ${routeLabel(value)}.`,
      autosaveKey(lib, 'usenet-route'),
    );
  }

  async function patch(body: LibraryPatch, note: string, statusKey: string) {
    const lib = selected;
    if (!lib) return;
    setAutosaveState(statusKey, 'saving');
    busy = true;
    try {
      replace(await api.updateLibrary(lib.id, body));
      setAutosaveState(statusKey, 'saved');
      pushToast(note, 'success');
    } catch (err) {
      setAutosaveState(statusKey, 'error');
      pushToast(errorText(err), 'danger');
      if (selected?.id === lib.id) reseed(lib);
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
    statusKey?: string,
  ): Promise<boolean> {
    const lib = selected;
    if (!lib) return false;
    if (statusKey) setAutosaveState(statusKey, 'saving');
    busy = true;
    try {
      replace(await api.setLibraryIndexer(lib.id, row.indexer_id, { enabled, categories }));
      if (statusKey) setAutosaveState(statusKey, 'saved');
      pushToast(note, 'success');
      return true;
    } catch (err) {
      if (statusKey) setAutosaveState(statusKey, 'error');
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
      autosaveKey(lib, `indexer-${row.indexer_id}`),
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

  function openAddLibrary() {
    const kind = creatableKinds[0] ?? 'movie';
    adding = { kind, name: '', root: '', provider: providersFor(kind)[0]?.id ?? '' };
  }

  /** Reseed the provider and root suggestions when the staged kind changes. */
  function stageKind(kind: LibraryKind) {
    if (!adding) return;
    const eligible = providersFor(kind);
    adding = {
      ...adding,
      kind,
      provider: eligible.some((p) => p.id === adding.provider)
        ? adding.provider
        : (eligible[0]?.id ?? ''),
    };
  }

  function stageName(name: string) {
    if (!adding) return;
    // The root suggestion follows the name until the user edits the root
    // themselves — a prefilled path they never think about is the common case.
    const suggested = 'library/' + adding.name.trim().replace(/[\\/]/g, ' ').trim();
    const followed = adding.root === '' || adding.root === suggested;
    adding = {
      ...adding,
      name,
      root: followed ? 'library/' + name.trim().replace(/[\\/]/g, ' ').trim() : adding.root,
    };
  }

  async function createLibrary() {
    if (!adding) return;
    busy = true;
    try {
      const created = await api.createLibrary({
        kind: adding.kind,
        name: adding.name.trim(),
        root_path: adding.root.trim(),
        provider: adding.provider,
      });
      libraries = [...libraries, created];
      selectedID = created.id;
      adding = null;
      pushToast(`Added the ${created.name} library.`, 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busy = false;
    }
  }

  async function deleteLibrary() {
    const lib = selected;
    if (!lib) return;
    busy = true;
    try {
      await api.deleteLibrary(lib.id);
      libraries = libraries.filter((l) => l.id !== lib.id);
      selectedID = libraries[0]?.id ?? null;
      confirmingDelete = false;
      pushToast(`Removed the ${lib.name} library.`, 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busy = false;
    }
  }

  /** Why the selected library cannot be deleted, or null when it can. */
  let deleteBlocked = $derived.by(() => {
    const lib = selected;
    if (!lib) return null;
    if (lib.kind === 'adult') return 'Adult libraries are managed by the adult module switch.';
    if (lib.is_default) return 'This is the default library for its kind. Make another library the default first.';
    if (lib.item_count > 0)
      return `The library still has ${lib.item_count} item${lib.item_count === 1 ? '' : 's'}. Move them to another library first.`;
    return null;
  });

  async function rescan() {
    if (scanning) return;
    scanning = true;
    scanMessage = null;
    try {
      await api.rescan();
      const summary = await api.awaitScan();
      const message = `Scan finished: ${summary.media_files} files in the library, ${summary.unmatched} unmatched.`;
      scanMessage = message;
      pushToast(message, summary.unmatched > 0 ? 'warning' : 'success');
    } catch (err) {
      scanMessage = `Scan failed: ${errorText(err)}`;
      pushToast(errorText(err), 'danger');
    } finally {
      scanning = false;
    }
  }
</script>

{#snippet autosaveStatus(key: string)}
  {#if autosaveStates[key]}
    {@const status = autosaveStates[key]}
    <span
      data-autosave-status={key}
      aria-live="polite"
      class="text-xs {status === 'saved'
        ? 'text-success'
        : status === 'error'
          ? 'text-danger'
          : 'text-ink-muted'}">
      {status === 'saving' ? 'Saving…' : status === 'saved' ? 'Saved' : 'Error'}
    </span>
  {/if}
{/snippet}

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
          onclick={() => (selectedID = option.id)}
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
          {#if option.is_default}
            <span class="micro-label" title="Default {option.kind} library">default</span>
          {/if}
        </button>
      {/each}
      <Button variant="ghost" size="sm" onclick={openAddLibrary}>
        <Icon name="plus" size={14} />
        Add library
      </Button>
    </div>

    <SettingsCard
      title="Library scan"
      description="Find new media under the storage root and refresh the library counts.">
      <div class="flex flex-wrap items-center gap-3">
        <Button variant="secondary" disabled={scanning} onclick={rescan}>
          <Icon name="refresh" size={14} />
          {scanning ? 'Scanning…' : 'Rescan library'}
        </Button>
        {#if scanMessage}
          <p class="text-sm text-ink-secondary" aria-live="polite">{scanMessage}</p>
        {/if}
      </div>
    </SettingsCard>

    <SettingsCard
      title={lib.name}
      description="Where this library lives, and what it grabs when an item names no profile of its own.">
      <Field
        label="Name"
        for="library-name"
        help="The label the pills and the add dialog show. Press Enter to save.">
        {#snippet note()}
          {@render autosaveStatus(autosaveKey(lib, 'name'))}
        {/snippet}
        <TextInput
          id="library-name"
          bind:value={nameDraft}
          onkeydown={(event) => {
            if (event.key === 'Enter') commitName();
          }} />
      </Field>

      <Field
        label="Root path"
        for="library-root"
        help="Relative to the storage root. Moving the whole library is Settings → Storage's job, so it is read-only here.">
        <div title={lib.root_path}>
          <TextInput id="library-root" value={lib.root_path} readonly mono />
        </div>
      </Field>

      <Field
        label="Metadata provider"
        for="library-provider"
        help="Which provider refreshes this library's items.">
        {#snippet note()}
          {@render autosaveStatus(autosaveKey(lib, 'provider'))}
        {/snippet}
        <select
          id="library-provider"
          value={lib.provider}
          disabled={busy || providersFor(lib.kind).length <= 1}
          onchange={(event) =>
            patch(
              { provider: event.currentTarget.value },
              `${lib.name} uses ${providerName(event.currentTarget.value)} for metadata.`,
              autosaveKey(lib, 'provider'),
            )}
          class="{SELECT_CLASS} border-border-strong">
          {#each providersFor(lib.kind) as option (option.id)}
            <option value={option.id}>{option.name}</option>
          {/each}
        </select>
      </Field>

      <div class="flex flex-wrap items-center gap-3">
        {#if lib.is_default}
          <Badge tone="accent">Default {lib.kind} library</Badge>
        {:else}
          <Button
            variant="secondary"
            size="sm"
            disabled={busy}
            onclick={() =>
              patch(
                { is_default: true },
                `${lib.name} is now the default ${lib.kind} library.`,
                autosaveKey(lib, 'default'),
              )}>
            Make default
          </Button>
          {@render autosaveStatus(autosaveKey(lib, 'default'))}
        {/if}
        <span class="mx-1 hidden h-5 w-px shrink-0 bg-border sm:block"></span>
        <div title={deleteBlocked ?? ''}>
          <Button
            variant="danger"
            size="sm"
            disabled={busy || deleteBlocked !== null}
            onclick={() => (confirmingDelete = true)}>
            Delete library
          </Button>
        </div>
        {#if deleteBlocked}
          <p class="text-sm text-ink-muted">{deleteBlocked}</p>
        {/if}
      </div>

      <Field
        label="Default quality profile"
        for="library-profile"
        help="Used for items in this library that name no profile. An item's own profile always wins.">
        {#snippet note()}
          {@render autosaveStatus(autosaveKey(lib, 'profile'))}
        {/snippet}
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
                aria-hidden="true"
                class="size-2 shrink-0 rounded-full {row.indexer_enabled
                  ? 'bg-success'
                  : 'bg-ink-muted'}">
              </span>

              <div class="flex min-w-0 flex-1 flex-col gap-1">
                <p class="flex flex-wrap items-center gap-2">
                  <span class="truncate text-base font-medium text-ink" title={row.name}>{row.name}</span>
                  <Badge mono tone={row.type === 'torznab' ? 'accent' : 'info'}>{row.type}</Badge>
                  <Badge tone={row.indexer_enabled ? 'success' : 'neutral'}>
                    {row.indexer_enabled ? 'Indexer enabled' : 'Indexer disabled'}
                  </Badge>
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

              <div class="flex w-full shrink-0 flex-col items-end gap-1 sm:w-auto">
                {@render autosaveStatus(autosaveKey(lib, `indexer-${row.indexer_id}`))}
                <Toggle
                  checked={row.enabled}
                  labelHidden
                  label="Search {row.name} for {lib.name}"
                  disabled={busy}
                  onchange={(next) => toggleIndexer(lib, row, next)} />
              </div>
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
        {@render autosaveStatus(autosaveKey(lib, 'torrent-route'))}
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
        {@render autosaveStatus(autosaveKey(lib, 'usenet-route'))}
      </Field>
    </SettingsCard>

    <SettingsCard title="Reach" description="Where this library shows up outside Caravan.">
      <div class="flex flex-wrap items-center gap-3">
        <Toggle
          checked={lib.dlna_visible}
          label="Share over DLNA"
          disabled={busy}
          onchange={(next) =>
            patch(
              { dlna_visible: next },
              next ? `${lib.name} is shared over DLNA.` : `${lib.name} is hidden from DLNA.`,
              autosaveKey(lib, 'dlna'),
            )} />
        {@render autosaveStatus(autosaveKey(lib, 'dlna'))}
      </div>
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
      <div class="flex w-full flex-wrap items-center justify-end gap-2">
        {#if row.categories_overridden}
          <Button variant="ghost" disabled={busy} onclick={clearCategories}>Use the indexer's</Button>
          <span class="mx-1 hidden h-5 w-px shrink-0 bg-border sm:block"></span>
        {/if}
        <Button variant="ghost" disabled={busy} onclick={() => (editing = null)}>Cancel</Button>
        <Button variant="primary" disabled={busy} onclick={saveCategories}>
          <Icon name="check" size={14} />
          Save
        </Button>
      </div>
    {/snippet}
  </Modal>
{/if}

{#if adding}
  {@const draft = adding}
  <Modal title="Add library" width="max-w-lg" onclose={() => (adding = null)}>
    <div class="flex flex-col gap-4 p-4">
      <Field label="Kind" for="new-library-kind" help="What the library holds. This cannot change later.">
        <select
          id="new-library-kind"
          value={draft.kind}
          onchange={(event) => stageKind(event.currentTarget.value as LibraryKind)}
          class="{SELECT_CLASS} border-border-strong">
          {#each creatableKinds as k (k)}
            <option value={k}>{k === 'movie' ? 'Movies' : k === 'tv' ? 'Series' : 'Adult'}</option>
          {/each}
        </select>
      </Field>

      <Field label="Name" for="new-library-name" help="The label the app shows for this library.">
        <TextInput
          id="new-library-name"
          value={draft.name}
          placeholder="Anime"
          oninput={(event) => stageName((event.currentTarget as HTMLInputElement).value)} />
      </Field>

      <Field
        label="Root path"
        for="new-library-root"
        help="Relative to the storage root, under library/. Created on the first import.">
        <TextInput id="new-library-root" bind:value={draft.root} mono placeholder="library/Anime" />
      </Field>

      {#if providersFor(draft.kind).length > 1}
        <Field label="Metadata provider" for="new-library-provider" help="Which provider refreshes this library's items.">
          <select
            id="new-library-provider"
            bind:value={draft.provider}
            class="{SELECT_CLASS} border-border-strong">
            {#each providersFor(draft.kind) as option (option.id)}
              <option value={option.id}>{option.name}</option>
            {/each}
          </select>
        </Field>
      {/if}

      <p class="text-sm text-ink-secondary">
        New libraries start hidden from DLNA; share them from the library's Reach card.
      </p>
    </div>

    {#snippet footer()}
      <div class="flex w-full flex-wrap items-center justify-end gap-2">
        <Button variant="ghost" disabled={busy} onclick={() => (adding = null)}>Cancel</Button>
        <Button
          variant="primary"
          disabled={busy || draft.name.trim() === '' || draft.root.trim() === ''}
          onclick={createLibrary}>
          <Icon name="check" size={14} />
          Add library
        </Button>
      </div>
    {/snippet}
  </Modal>
{/if}

{#if confirmingDelete && selected}
  {@const lib = selected}
  <Modal title="Delete {lib.name}?" width="max-w-md" onclose={() => (confirmingDelete = false)}>
    <div class="flex flex-col gap-2 p-4">
      <p class="text-md text-ink">
        The library is empty, so no files are touched. The row and its per-library settings are
        removed; the folder on disk, if it exists, stays where it is.
      </p>
    </div>
    {#snippet footer()}
      <div class="flex w-full flex-wrap items-center justify-end gap-2">
        <Button variant="ghost" disabled={busy} onclick={() => (confirmingDelete = false)}>
          Cancel
        </Button>
        <Button variant="danger" disabled={busy} onclick={deleteLibrary}>Delete library</Button>
      </div>
    {/snippet}
  </Modal>
{/if}
