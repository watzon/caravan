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
   * It is also where a library is switched off and narrowed to named accounts
   * (PLAN Part 3 phase 5). Those two controls used to be one module-wide
   * switch and one module-wide roster on a page of their own; a library is the
   * object they always described, so they moved onto its card and the page
   * dissolved. Nothing here asks what KIND a library is before offering them —
   * an adult shelf and a children's shelf are the same object with the same
   * questions, which is the whole point of the generalization.
   *
   * Every control saves on change rather than through a per-card Save button.
   * The switcher pills swap the whole screen, so staged edits would either be
   * silently discarded on a switch or have to block it; and the library writes
   * answer with the library's whole state, so one response re-renders every
   * card. A failed write therefore leaves the screen showing what the server
   * still holds, never an edit that did not land. The access pair is the one
   * exception and says so where it is written: it answers with itself.
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
    type LibraryAccess,
    type LibraryIndexer,
    type LibraryKind,
    type LibraryMember,
    type LibraryPatch,
    type MetadataProviderInfo,
    type Protocol,
    type QualityProfile,
    type Settings,
  } from '../api/types';
  import { describeType } from '../downloadClient';
  import { formatCategories, parseCategories } from '../indexer';
  import { session } from '../state/session.svelte';
  import { pushToast } from '../state/toast.svelte';
  import Badge from './Badge.svelte';
  import Banner from './Banner.svelte';
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
  let adding = $state<{
    kind: LibraryKind;
    name: string;
    root: string;
    /** A chain of one. It grows on the identity card once the library exists. */
    providers: string[];
  } | null>(null);
  /** Set while the delete confirmation is open for the selected library. */
  let confirmingDelete = $state(false);

  /** The (library, indexer) pair whose categories the modal is editing. */
  let editing = $state<LibraryIndexer | null>(null);
  let categoryText = $state('');

  /** The Access card's roster, and which library it describes. */
  let access = $state<LibraryAccess | null>(null);
  let accessFor = $state<number | null>(null);
  let accessError = $state<string | null>(null);
  /** True while an access write is in flight, so only that card goes quiet. */
  let savingAccess = $state(false);

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

  /**
   * The kinds the create form may offer: whatever some provider can serve —
   * and `adult` always.
   *
   * Adult is the exception because creating the library is what opens the
   * stash-box instance CRUD (those routes live under /adult and are absent
   * until an adult library exists). Filtering it out for want of a configured
   * box would make the door that opens the box unreachable, so the form warns
   * instead and the server accepts the bare legacy id on a boxless install.
   */
  let creatableKinds = $derived(
    (['movie', 'tv', 'adult'] as LibraryKind[]).filter(
      (k) => k === 'adult' || providers.some((p) => p.kinds.includes(k)),
    ),
  );

  function providersFor(k: LibraryKind): MetadataProviderInfo[] {
    return providers.filter((p) => p.kinds.includes(k));
  }

  function providerName(id: string): string {
    return providers.find((p) => p.id === id)?.name ?? id;
  }

  /**
   * The chain editor writes the WHOLE list every time, never a delta.
   *
   * Order is the setting — the first provider that recognizes a title wins a
   * scan — so "move this one up" and "add this one" are both just a new list,
   * and the server validates it as one thing: non-empty, no duplicates, every
   * element serving the kind. Sending a delta would ask the screen to guess
   * what the stored list was, and a rejected write would leave it guessing
   * wrong.
   */
  function saveChain(lib: Library, next: string[], note: string) {
    void patch({ providers: next }, note, autosaveKey(lib, 'provider'));
  }

  /** Swap a chain entry with its neighbour; `to` outside the list is a no-op. */
  function moveProvider(lib: Library, from: number, to: number) {
    if (to < 0 || to >= lib.providers.length) return;
    const next = [...lib.providers];
    const moved = next[from]!;
    next[from] = next[to]!;
    next[to] = moved;
    saveChain(
      lib,
      next,
      to === 0
        ? `${providerName(moved)} identifies ${lib.name} first.`
        : `${lib.name} asks ${providerName(moved)} ${to + 1}${to === 1 ? 'nd' : to === 2 ? 'rd' : 'th'}.`,
    );
  }

  function removeProvider(lib: Library, id: string) {
    // A library with no provider could never identify anything, so the last
    // one is not removable — the control is disabled rather than the write
    // being left to fail.
    if (lib.providers.length <= 1) return;
    saveChain(
      lib,
      lib.providers.filter((p) => p !== id),
      `${lib.name} no longer asks ${providerName(id)}.`,
    );
  }

  function addProvider(lib: Library, id: string) {
    if (id === '' || lib.providers.includes(id)) return;
    saveChain(lib, [...lib.providers, id], `${lib.name} also asks ${providerName(id)}.`);
  }

  /** Providers eligible for this library's kind that its chain does not name yet. */
  function addableProviders(lib: Library): MetadataProviderInfo[] {
    return providersFor(lib.kind).filter((p) => !lib.providers.includes(p.id));
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

  // The roster belongs to one library, so switching pills re-reads it. The
  // guard is `accessFor` rather than a flag: loadAccess claims the id before it
  // awaits, so this cannot queue a second read for the library already showing.
  $effect(() => {
    const lib = selected;
    if (lib && accessFor !== lib.id) void loadAccess(lib.id);
  });

  /**
   * Read one library's access decision.
   *
   * Read for every library, restricted or not, because the toggle that
   * restricts one has to carry the allow-list with it — the PUT is the whole
   * decision — and a library that was restricted before, then opened, still
   * holds the rows somebody named.
   */
  async function loadAccess(id: number) {
    accessFor = id;
    access = null;
    accessError = null;
    try {
      const loaded = await api.getLibraryAccess(id);
      // A faster pill may have moved on while this was in flight; rendering
      // one library's roster under another's name is the worst thing this card
      // could do.
      if (accessFor !== id) return;
      access = loaded;
    } catch (err) {
      if (accessFor !== id) return;
      accessError = errorText(err);
    }
  }

  /**
   * The ids the server currently holds a grant row for, whatever the role. An
   * admin's row is normally absent — they reach the library through the role —
   * but one that exists is a decision somebody made, and the next write must
   * not quietly drop it.
   */
  function grantedIDs(current: LibraryAccess): number[] {
    return current.users.filter((u) => u.granted).map((u) => u.id);
  }

  /**
   * Write the whole decision: the flag and the complete allow-list, in one
   * request. Split in two there is a window in which the library is restricted
   * to nobody, and a member watching the screen sees a shelf vanish that was
   * never meant to leave.
   */
  async function saveAccess(lib: Library, restricted: boolean, userIDs: number[], note: string) {
    const statusKey = autosaveKey(lib, 'access');
    setAutosaveState(statusKey, 'saving');
    savingAccess = true;
    try {
      const updated = await api.setLibraryAccess(lib.id, { restricted, user_ids: userIDs });
      if (accessFor === lib.id) access = updated;
      // Restricting clears dlna_visible in the same server transaction — DLNA
      // has no accounts, so the two cannot both be true — and the access answer
      // does not carry the library row, so the clearing is reflected here or
      // the Reach card keeps showing a share that is already down.
      replace({
        ...lib,
        restricted: updated.restricted,
        dlna_visible: updated.restricted ? false : lib.dlna_visible,
      });
      setAutosaveState(statusKey, 'saved');
      pushToast(note, 'success');
    } catch (err) {
      setAutosaveState(statusKey, 'error');
      pushToast(errorText(err), 'danger');
    } finally {
      savingAccess = false;
    }
  }

  /**
   * The master switch, then the session.
   *
   * Switching a library off hides it from EVERYONE including the admin who
   * did it, and the nav item, the Explore scopes and the request form all read
   * that from /auth/me. Without the re-read they would keep offering a shelf
   * that no longer answers until the next full page load.
   */
  async function setActive(lib: Library, next: boolean) {
    await patch(
      { active: next },
      next ? `${lib.name} is active again.` : `${lib.name} is switched off.`,
      autosaveKey(lib, 'active'),
    );
    await session.refresh();
  }

  function toggleRestricted(lib: Library, next: boolean) {
    void saveAccess(
      lib,
      next,
      access ? grantedIDs(access) : [],
      next
        ? `${lib.name} is limited to the accounts you name.`
        : `${lib.name} is open to every account.`,
    );
  }

  function toggleMember(lib: Library, user: LibraryMember, next: boolean) {
    const current = access;
    if (!current) return;
    const ids = grantedIDs(current).filter((id) => id !== user.id);
    if (next) ids.push(user.id);
    void saveAccess(
      lib,
      current.restricted,
      ids,
      next
        ? `${user.username} can see ${lib.name}.`
        : `${user.username} can no longer see ${lib.name}.`,
    );
  }

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
    adding = { kind, name: '', root: '', providers: chainSeed(kind) };
  }

  /** The chain a new library of this kind starts with: its first eligible provider. */
  function chainSeed(kind: LibraryKind): string[] {
    const head = providersFor(kind)[0]?.id;
    return head ? [head] : [];
  }

  /** Reseed the provider and root suggestions when the staged kind changes. */
  function stageKind(kind: LibraryKind) {
    if (!adding) return;
    const eligible = providersFor(kind);
    const head = adding.providers[0];
    adding = {
      ...adding,
      kind,
      providers: head && eligible.some((p) => p.id === head) ? [head] : chainSeed(kind),
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
        providers: adding.providers,
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

  /**
   * Why the selected library cannot be deleted, or null when it can.
   *
   * An adult library is not a special case here any more: the server dropped
   * its own refusal when the module switch dissolved, and the two guards below
   * are the same two every other kind answers to. Switching a library off is
   * the non-destructive "off" that the module switch used to be.
   */
  let deleteBlocked = $derived.by(() => {
    const lib = selected;
    if (!lib) return null;
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
               GET /libraries returned a row. The server omits every row this
               caller may not manage, so this list needs no rule of its own —
               and an INACTIVE row still arrives, because the switch that
               undoes it lives on the card behind this pill. -->
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
      {#snippet action()}
        {#if !lib.active}
          <Badge tone="warning">Inactive</Badge>
        {/if}
      {/snippet}

      <!-- The switch stays in the ungreyed card on purpose: an inactive library
           is dormant everywhere else, and the one control that undoes that has
           to stay obviously reachable. -->
      <div class="flex flex-wrap items-center gap-3">
        <Toggle
          checked={lib.active}
          label="Library active"
          disabled={busy}
          onchange={(next) => setActive(lib, next)} />
        {@render autosaveStatus(autosaveKey(lib, 'active'))}
      </div>
      <p class="text-sm text-ink-secondary">
        Turning this off hides {lib.name} everywhere — the sidebar, Discover, requests, the calendar,
        search and the DLNA tree — and stops its scans and automatic searches. It deletes nothing:
        the items and the files stay exactly where they are, and turning it back on finds them.
      </p>

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

      <!-- No `for`: the field is a list plus an optional control, and the
           control disappears once every eligible provider is on the chain — a
           label pointing at an element that is not there names nothing. The
           list and the select carry their own accessible names instead. -->
      <Field
        label="Metadata providers"
        help="Asked in this order. The first that recognizes a title identifies it; the add dialog searches all of them.">
        {#snippet note()}
          {@render autosaveStatus(autosaveKey(lib, 'provider'))}
        {/snippet}
        <ol class="flex flex-col gap-2" aria-label="Provider chain for {lib.name}">
          {#each lib.providers as id, index (id)}
            <li
              data-provider-row={id}
              class="flex items-center gap-2 rounded-md border border-border bg-surface px-3 py-2">
              <!-- The position is the setting, so it is shown rather than left
                   to be counted off the rows. -->
              <span class="micro-label w-4 shrink-0 text-ink-muted">{index + 1}</span>
              <span class="min-w-0 flex-1 truncate text-base text-ink">{providerName(id)}</span>
              <Button
                variant="ghost"
                size="sm"
                disabled={busy || index === 0}
                onclick={() => moveProvider(lib, index, index - 1)}>
                ↑<span class="sr-only">Move {providerName(id)} earlier</span>
              </Button>
              <Button
                variant="ghost"
                size="sm"
                disabled={busy || index === lib.providers.length - 1}
                onclick={() => moveProvider(lib, index, index + 1)}>
                ↓<span class="sr-only">Move {providerName(id)} later</span>
              </Button>
              <!-- A library with an empty chain could identify nothing, so the
                   last provider is not removable. -->
              <Button
                variant="ghost"
                size="sm"
                disabled={busy || lib.providers.length <= 1}
                onclick={() => removeProvider(lib, id)}>
                Remove<span class="sr-only"> {providerName(id)} from the chain</span>
              </Button>
            </li>
          {/each}
        </ol>
        {#if addableProviders(lib).length > 0}
          <select
            id="library-provider-add"
            value=""
            aria-label="Add a provider to {lib.name}"
            disabled={busy}
            onchange={(event) => {
              addProvider(lib, event.currentTarget.value);
              // The select is a verb, not a value: it goes back to its prompt
              // so the next add starts from the same place.
              event.currentTarget.value = '';
            }}
            class="{SELECT_CLASS} border-border-strong">
            <option value="">Add provider…</option>
            {#each addableProviders(lib) as option (option.id)}
              <option value={option.id}>{option.name}</option>
            {/each}
          </select>
        {/if}
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

    <!-- Everything below the switch describes what the library DOES, and an
         inactive library does none of it. Greyed rather than hidden: these are
         still the settings it will come back with. The Access card is inside
         the veil but stays editable on purpose — fixing who may see a library
         BEFORE switching it back on is the order somebody would want. -->
    <div data-library-behaviour class="flex flex-col gap-5 {lib.active ? '' : 'opacity-60'}">
      <SettingsCard
        title="Access"
        description="Who reaches {lib.name}. Admins always do; every other account is one grant.">
        <div class="flex flex-wrap items-center gap-3">
          <Toggle
            checked={lib.restricted}
            label="Limit to named accounts"
            disabled={busy || savingAccess}
            onchange={(next) => toggleRestricted(lib, next)} />
          {@render autosaveStatus(autosaveKey(lib, 'access'))}
        </div>
        <p class="text-sm text-ink-secondary">
          While this is off, every account sees {lib.name}. Turning it on hides the library from
          everyone but the admins and the accounts named below, and takes it off DLNA — that share
          has no accounts to name, so it cannot express this at all.
        </p>

        {#if lib.restricted}
          {#if accessError}
            <LoadError message={accessError} onretry={() => loadAccess(lib.id)} />
          {:else if access === null || accessFor !== lib.id}
            <div class="flex flex-col gap-2">
              {#each Array.from({ length: 2 }) as _, i (i)}
                <Skeleton class="h-12 w-full rounded-md" />
              {/each}
            </div>
          {:else if access.users.length === 0}
            <EmptyState
              icon="inbox"
              title="No accounts yet"
              message="This Caravan is open, so anyone who can reach it is an admin and already sees every library. Add accounts under Settings → Users to decide who does." />
          {:else}
            <ul class="flex flex-col gap-2">
              {#each access.users as user (user.id)}
                <li
                  data-access-row={user.id}
                  class="flex flex-wrap items-center gap-3 rounded-md border border-border bg-surface px-3 py-3">
                  <div class="min-w-0 flex-1">
                    <p class="flex flex-wrap items-center gap-2">
                      <span class="truncate text-base font-medium text-ink" title={user.username}>
                        {user.username}
                      </span>
                      <Badge mono tone={user.role === 'admin' ? 'accent' : 'neutral'}>
                        {user.role === 'admin' ? 'ADMIN' : 'MEMBER'}
                      </Badge>
                    </p>
                  </div>
                  <div class="flex w-full shrink-0 items-center justify-end gap-2 sm:w-auto">
                    <!-- A checkbox that changes nothing is a lie about who can
                         see the shelf: an admin reaches it through the role. -->
                    {#if user.always_granted}
                      <span class="text-sm text-ink-secondary">Always has access</span>
                    {:else}
                      <Toggle
                        checked={user.granted}
                        disabled={busy || savingAccess}
                        labelHidden
                        label="{lib.name} for {user.username}"
                        onchange={(next) => toggleMember(lib, user, next)} />
                    {/if}
                  </div>
                </li>
              {/each}
            </ul>
          {/if}
        {/if}
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

        <!-- Restricting a library clears this share, so the two can only be
             true together after somebody switched DLNA back on afterwards.
             That is allowed — it is a decision, not an accident — and this says
             out loud what the decision means. -->
        {#if lib.restricted && lib.dlna_visible}
          <Banner
            tone="warning"
            icon="warning"
            title="{lib.name} is on the network"
            message="This library is limited to named accounts, and it is also shared over DLNA. DLNA has no accounts — every device on this network can browse it. Turn the share off above if that is not what you meant." />
        {/if}

        {#if lib.kind === 'adult'}
          <p class="text-sm text-ink-secondary">
            <code class="font-mono text-sm">caravan prepare</code> leaves adult libraries out of a
            prepared drive. Passing <code class="font-mono text-sm">--include-adult</code> is the
            only way {lib.name} goes along.
          </p>
        {/if}
      </SettingsCard>
    </div>
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
        <Field
          label="Metadata provider"
          for="new-library-provider"
          help="Which provider identifies this library's items. Add more, and reorder them, from the library's own card afterwards.">
          <select
            id="new-library-provider"
            value={draft.providers[0] ?? ''}
            onchange={(event) => (draft.providers = [event.currentTarget.value])}
            class="{SELECT_CLASS} border-border-strong">
            {#each providersFor(draft.kind) as option (option.id)}
              <option value={option.id}>{option.name}</option>
            {/each}
          </select>
        </Field>
      {/if}

      <!-- A warning, never a block (PLAN Part 3 phase 4): the stash-box
           instance CRUD lives under /adult and only appears once an adult
           library does, so the library necessarily comes first. A library whose
           chain resolves to no box parks its scans rather than failing them. -->
      {#if draft.kind === 'adult' && providersFor('adult').length === 0}
        <Banner
          tone="warning"
          icon="warning"
          title="No stash-box endpoint yet"
          message="Adult libraries get their sites and scenes from a stash-box endpoint. Add one under Settings → Metadata — scans wait until one answers.">
          {#snippet action()}
            <a href="/settings/metadata" class="text-sm text-accent-text hover:underline">
              Metadata
            </a>
          {/snippet}
        </Banner>
      {/if}

      <p class="text-sm text-ink-secondary">
        New libraries start hidden from DLNA; share them from the library's Reach card.
        {#if draft.kind === 'adult'}
          An adult library also starts limited to the admins — name who else reaches it on its
          Access card.
        {/if}
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
