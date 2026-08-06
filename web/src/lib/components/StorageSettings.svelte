<script lang="ts">
  /**
   * Storage settings (SPEC §10) — the two ways to change where the library
   * lives, kept visibly apart because only one of them touches media.
   *
   * Re-point changes where Caravan looks. Every stored path is relative to the
   * root, so it is instant and reversible, and the files stay exactly where
   * they are. That is the operation for "the drive letter changed" and for
   * "I already copied everything myself".
   *
   * Migrate moves the files. It is hours of work on a durable job, so this
   * screen polls a row rather than holding a request open: closing the tab
   * does not stop it, and reopening it shows where the move got to.
   */
  import { onDestroy, onMount, untrack } from 'svelte';
  import { api, errorText } from '../api/client';
  import {
    SETTING_STORAGE_ROOT,
    type Settings,
    type StorageMigration,
  } from '../api/types';
  import { formatBytes } from '../format';
  import { pushToast } from '../state/toast.svelte';
  import { system } from '../state/system.svelte';
  import Banner from './Banner.svelte';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';
  import Modal from './Modal.svelte';
  import ProgressBar from './ProgressBar.svelte';
  import TextInput from './TextInput.svelte';
  import DatabaseSettings from './DatabaseSettings.svelte';

  interface Props {
    settings: Settings;
  }

  let { settings }: Props = $props();

  /** Matches the convert queue's cadence: a file-by-file move, not a spinner. */
  const POLL_MS = 2000;

  // Derived from the system status rather than held locally, so a root that
  // moved — by a re-point here, or by a migration finishing in the background —
  // is on screen without this component tracking it.
  let currentRoot = $derived(system.status?.storage_root || settings[SETTING_STORAGE_ROOT] || '');
  let newRoot = $state('');
  let busy = $state(false);
  let scanning = $state(false);
  let confirming = $state<'repoint' | 'migrate' | 'permanent-delete' | null>(null);
  let warnings = $state<string[]>([]);
  let restartRequired = $state(false);
  let migration = $state<StorageMigration | null>(null);

  const namingKeys = {
    recycle: 'recycle_retention_days',
    movieFolder: 'movie_folder_format',
    movieFile: 'movie_file_format',
    seriesFolder: 'series_folder_format',
    seasonFolder: 'season_folder_format',
    episodeFile: 'episode_file_format',
  } as const;
  let recycleRetentionDays = $state(untrack(() => settings[namingKeys.recycle] || '0'));
  let movieFolderFormat = $state(untrack(() => settings[namingKeys.movieFolder] || '{title}{year}'));
  let movieFileFormat = $state(untrack(() => settings[namingKeys.movieFile] || '{title}{year}{edition}'));
  let seriesFolderFormat = $state(untrack(() => settings[namingKeys.seriesFolder] || '{title}{year}'));
  let seasonFolderFormat = $state(untrack(() => settings[namingKeys.seasonFolder] || 'Season {season:02}'));
  let episodeFileFormat = $state(untrack(() => settings[namingKeys.episodeFile] || '{series}{year} - {episode}{title}'));
  let namingBusy = $state(false);

  function preview(format: string, tokens: Record<string, string>) {
    return format.replace(/\{([^}]+)\}/g, (_, token) => tokens[token] ?? `{${token}}`);
  }

  let moviePreview = $derived(
    preview(movieFileFormat, { title: 'Big Buck Bunny', year: ' (2008)', edition: " - Director's Cut" }) +
      '.mkv',
  );

  function namingError() {
    const retention = Number(recycleRetentionDays);
    if (!Number.isInteger(retention) || retention < 0 || retention > 3650) {
      return 'Recycle retention must be an integer between 0 and 3650.';
    }
    const formats: Array<[string, string, string[], string[]]> = [
      ['Movie folder format', movieFolderFormat, ['title', 'year'], ['title']],
      ['Movie file format', movieFileFormat, ['title', 'year', 'edition'], ['title']],
      ['Series folder format', seriesFolderFormat, ['title', 'year'], ['title']],
      ['Season folder format', seasonFolderFormat, ['season', 'season:02'], ['season']],
      ['Episode file format', episodeFileFormat, ['series', 'year', 'episode', 'title'], ['series', 'episode']],
    ];
    for (const [label, format, allowed, required] of formats) {
      const tokens = [...format.matchAll(/\{([^}]+)\}/g)].map((match) => match[1]);
      if (!format || tokens.some((token) => !allowed.includes(token))) return `${label} has an invalid token.`;
      if (required.some((token) => token === 'season'
        ? !tokens.includes('season') && !tokens.includes('season:02')
        : !tokens.includes(token))) return `${label} is missing a required token.`;
    }
    return '';
  }

  function namingSnapshot() {
    return JSON.stringify({
      recycleRetentionDays,
      movieFolderFormat,
      movieFileFormat,
      seriesFolderFormat,
      seasonFolderFormat,
      episodeFileFormat,
    });
  }

  let savedNaming = $state(untrack(() => namingSnapshot()));
  let namingChanged = $derived(namingSnapshot() !== savedNaming);
  let namingInvalid = $derived(namingError() !== '');
  let permanentDeletion = $derived(Number(recycleRetentionDays) === 0);
  let episodePreview = $derived(
    preview(episodeFileFormat, {
      series: 'Planet Earth II',
      year: ' (2016)',
      episode: 'S01E01',
      title: ' - Islands',
    }) + '.mkv',
  );

  async function saveNaming() {
    const error = namingError();
    if (error) {
      pushToast(error, 'danger');
      return;
    }
    confirming = null;
    namingBusy = true;
    try {
      await api.putSettings({
        [namingKeys.recycle]: String(recycleRetentionDays),
        [namingKeys.movieFolder]: movieFolderFormat,
        [namingKeys.movieFile]: movieFileFormat,
        [namingKeys.seriesFolder]: seriesFolderFormat,
        [namingKeys.seasonFolder]: seasonFolderFormat,
        [namingKeys.episodeFile]: episodeFileFormat,
      });
      savedNaming = namingSnapshot();
      pushToast('Recycle retention and naming settings saved.', 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      namingBusy = false;
    }
  }
  function requestNamingSave() {
    if (permanentDeletion) {
      confirming = 'permanent-delete';
      return;
    }
    void saveNaming();
  }

  let timer: ReturnType<typeof setInterval> | null = null;

  let running = $derived(
    migration !== null && (migration.status === 'queued' || migration.status === 'running'),
  );
  let progress = $derived(
    migration && migration.bytes_total > 0 ? migration.bytes_done / migration.bytes_total : 0,
  );
  /**
   * A prepared drive's config sets `storage_root: "."` and the settings table
   * keeps that literal value, because that is what makes the drive portable:
   * every path resolves against wherever the drive is mounted today, on any
   * machine (see cmd/caravan/prepare_test.go, which asserts exactly ".").
   *
   * Neither operation on this screen can honour that. Migrate refuses outright
   * — "the current storage root is not an absolute path" — and re-point only
   * accepts an absolute path, which would pin the drive to one computer and
   * make it stop working on the next one. So on a portable install both are
   * disabled and the reason is on screen, rather than offering a button that
   * always fails and one that quietly breaks the drive.
   */
  let portable = $derived(system.status?.mode === 'portable');
  let canAct = $derived(
    !portable && !busy && !running && newRoot.trim() !== '' && newRoot.trim() !== currentRoot,
  );

  async function loadMigration() {
    try {
      const status = await api.storageMigration();
      const changed = migration?.status !== status.migration?.status;
      migration = status.migration;
      if (status.restart_required) restartRequired = true;
      // A move that completed in the background moved the storage root with it,
      // so the shell's copy of it is now stale.
      if (changed && migration?.status === 'done') await system.refresh();
    } catch {
      // A poll that fails is not worth a toast every two seconds; the banner
      // for the operation the user actually started already carries errors.
    }
  }

  onMount(() => {
    void loadMigration();
    timer = setInterval(() => void loadMigration(), POLL_MS);
  });
  onDestroy(() => {
    if (timer) clearInterval(timer);
  });

  async function repoint() {
    confirming = null;
    busy = true;
    try {
      const result = await api.repointStorageRoot(newRoot.trim());
      newRoot = '';
      warnings = result.warnings;
      restartRequired = result.restart_required;
      await system.refresh();
      pushToast('Storage root re-pointed. No files were moved.', 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busy = false;
    }
  }

  async function migrate() {
    confirming = null;
    busy = true;
    try {
      migration = await api.migrateStorageRoot(newRoot.trim());
      warnings = [];
      pushToast('Migration queued. Downloads are paused while the files move.', 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busy = false;
    }
  }

  async function rescan() {
    scanning = true;
    try {
      await api.rescan();
      const summary = await api.awaitScan();
      await system.refresh();
      pushToast(
        `Scan finished: ${summary.media_files} files in the library, ${summary.unmatched} unmatched.`,
        summary.unmatched > 0 ? 'warning' : 'success',
      );
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      scanning = false;
    }
  }
</script>

<section class="flex flex-col gap-6">
  <Field
    label="Current storage root"
    for="settings-storage-root-current"

    help="Every path in the database is relative to this folder. That is what makes re-pointing instant and a rescan enough to rebuild the library.">
    <TextInput id="settings-storage-root-current" value={currentRoot} mono readonly />
  </Field>
  <div class="flex flex-col gap-4 rounded-md border border-border bg-surface p-4">
    <div>
      <h3 class="text-sm font-semibold">Recycle and library naming</h3>
      <p class="mt-1 text-sm text-muted">
        Deleted Caravan files can be retained under <code>recycle/&lt;UTC batch&gt;/</code>.
        Naming changes apply to new imports only.
      </p>
    </div>
    <Field
      label="Recycle retention (days)"
      for="settings-recycle-retention"
      help="0 permanently deletes future Caravan-owned media, posters and NFO files. Use 1 to 3650 days to retain them."
    >
      <TextInput id="settings-recycle-retention" type="number" min="0" max="3650" bind:value={recycleRetentionDays} />
    </Field>
    {#if permanentDeletion}
      <Banner
        tone="danger"
        icon="warning"
        title="Permanent deletion"
        message="With 0 days of retention, future Caravan deletions permanently remove Caravan-owned media, posters and NFO files instead of moving them to recycle." />
    {/if}
    <Field label="Movie folder format" for="settings-movie-folder-format" help={'Tokens: {title}, {year}.'}>
      <TextInput id="settings-movie-folder-format" bind:value={movieFolderFormat} mono />
    </Field>
    <Field label="Movie file format" for="settings-movie-file-format" help={'Tokens: {title}, {year}, {edition}.'}>
      <TextInput id="settings-movie-file-format" bind:value={movieFileFormat} mono />
    </Field>
    <Field label="Series folder format" for="settings-series-folder-format" help={'Tokens: {title}, {year}.'}>
      <TextInput id="settings-series-folder-format" bind:value={seriesFolderFormat} mono />
    </Field>
    <Field label="Season folder format" for="settings-season-folder-format" help={'Tokens: {season}, {season:02}.'}>
      <TextInput id="settings-season-folder-format" bind:value={seasonFolderFormat} mono />
    </Field>
    <Field
      label="Episode file format"
      for="settings-episode-file-format"
      help={'Tokens: {series}, {year}, {episode}, {title}.'}
    >
      <TextInput id="settings-episode-file-format" bind:value={episodeFileFormat} mono />
    </Field>
    <div class="rounded bg-base px-3 py-2 text-sm text-muted">
      <p>Movie preview: <code>{preview(movieFolderFormat, { title: 'Big Buck Bunny', year: ' (2008)' })}/{moviePreview}</code></p>
      <p>Episode preview: <code>{preview(seriesFolderFormat, { title: 'Planet Earth II', year: ' (2016)' })}/{preview(seasonFolderFormat, { season: '1', 'season:02': '01' })}/{episodePreview}</code></p>
    </div>
    <div>
      <Button
        variant={permanentDeletion ? 'danger' : 'primary'}
        onclick={requestNamingSave}
        disabled={namingBusy || !namingChanged || namingInvalid}
      >
        {namingBusy ? 'Saving…' : 'Save recycle and naming'}
      </Button>
    </div>
  </div>
  <DatabaseSettings />

  {#if portable}
    <Banner
      tone="info"
      icon="warning"
      title="This is a portable drive"
      message={'Caravan stores everything relative to the drive itself, which is what lets it work on any computer you plug it into. Re-pointing or moving the root would replace that with one machine’s absolute path and the drive would stop working elsewhere. To move this library to a server, copy the drive’s library folder across and run a rescan there.'} />
  {:else}
    <Field
      label="New storage root"
      for="settings-storage-root"
      help="An absolute path that already exists on this machine.">
      <TextInput id="settings-storage-root" bind:value={newRoot} mono placeholder="/data" />
    </Field>
  {/if}

  <div class="flex flex-wrap gap-2">
    {#if !portable}
      <Button variant="primary" disabled={!canAct} onclick={() => (confirming = 'repoint')}>
        <Icon name="check" size={14} />
        Re-point
      </Button>
      <Button variant="secondary" disabled={!canAct} onclick={() => (confirming = 'migrate')}>
        <Icon name="folder" size={14} />
        Move files here
      </Button>
    {/if}
    <Button variant="secondary" disabled={scanning || running} onclick={rescan}>
      <Icon name="refresh" size={14} />
      {scanning ? 'Scanning…' : 'Rescan library'}
    </Button>
  </div>

  {#each warnings as warning (warning)}
    <Banner tone="warning" icon="warning" title="Re-pointed anyway" message={warning} />
  {/each}

  {#if restartRequired}
    <Banner
      tone="warning"
      icon="warning"
      title="Restart to move the download queue"
      message="The library, artwork and media server are already using the new root. The download engine keeps writing under the old one until Caravan restarts." />
  {/if}

  {#if migration}
    <div class="flex flex-col gap-3 rounded-md border border-border bg-surface p-4">
      <div class="flex items-baseline justify-between gap-3">
        <p class="text-base font-semibold text-ink">
          {running ? 'Moving files' : 'Last migration'}
        </p>
        <p class="font-mono text-xs text-ink-secondary">
          {migration.files_done} / {migration.files_total} files
          · {formatBytes(migration.bytes_done)} of {formatBytes(migration.bytes_total)}
        </p>
      </div>
      <ProgressBar
        value={progress}
        tone={migration.status === 'done'
          ? 'success'
          : migration.status === 'queued' || migration.status === 'running'
            ? 'accent'
            : 'danger'}
        label="Storage migration progress" />
      <p class="font-mono text-xs break-all text-ink-secondary">
        {migration.source_root} → {migration.target_root}
      </p>

      {#if migration.status === 'queued'}
        <Banner tone="info" icon="warning" message="Queued. The move starts as soon as the worker picks it up." />
      {:else if migration.status === 'running'}
        <Banner
          tone="info"
          icon="warning"
          message="Downloads are paused while the files move. The storage root changes only once every file has arrived and been checked." />
      {:else if migration.status === 'done'}
        <Banner
          tone="success"
          icon="check"
          title="Migration finished"
          message="Every file arrived at the new root and the storage root now points at it. Restart Caravan to resume downloads there." />
      {:else if migration.status === 'rolled_back'}
        <Banner
          tone="warning"
          icon="warning"
          title="Migration rolled back"
          message={`${migration.error} Every file was put back under ${migration.source_root} and the storage root never moved.`} />
      {:else}
        <Banner
          tone="danger"
          icon="warning"
          title="Migration failed and could not be undone"
          message={`${migration.error} Part of the library is under each root; move the remaining files back by hand before starting another migration.`} />
      {/if}
    </div>
  {/if}
</section>

{#if confirming === 'permanent-delete'}
  <Modal title="Use permanent deletion?" width="max-w-md" onclose={() => (confirming = null)}>
    <div class="flex flex-col gap-3 px-4 py-4">
      <p class="text-base text-ink-secondary">
        Future Caravan deletions will permanently remove Caravan-owned media, posters and NFO files
        instead of moving them to recycle. This cannot be undone.
      </p>
      <p class="text-base text-ink-secondary">
        Saving this setting does not delete anything now or remove files already in recycle.
      </p>
    </div>
    {#snippet footer()}
      <Button variant="secondary" onclick={() => (confirming = null)}>Cancel</Button>
      <Button variant="danger" onclick={saveNaming}>Save with permanent deletion</Button>
    {/snippet}
  </Modal>
{/if}

{#if confirming === 'repoint'}
  <Modal title="Re-point the storage root?" width="max-w-md" onclose={() => (confirming = null)}>
    <div class="flex flex-col gap-3 px-4 py-4">
      <p class="text-base text-ink-secondary">
        Caravan will look for the library under <span class="font-mono break-all">{newRoot.trim()}</span>
        from now on. No files are moved.
      </p>
      <p class="text-base text-ink-secondary">
        If the media is not already there, the next rescan will report the whole
        library as missing. Use "Move files here" instead when the files still
        live under the old root.
      </p>
    </div>
    {#snippet footer()}
      <Button variant="secondary" onclick={() => (confirming = null)}>Cancel</Button>
      <Button variant="primary" onclick={repoint}>
        <Icon name="check" size={14} />
        Re-point
      </Button>
    {/snippet}
  </Modal>
{/if}

{#if confirming === 'migrate'}
  <Modal title="Move the library?" width="max-w-md" onclose={() => (confirming = null)}>
    <div class="flex flex-col gap-3 px-4 py-4">
      <p class="text-base text-ink-secondary">
        Caravan will move the library and incomplete folders from
        <span class="font-mono break-all">{currentRoot}</span> to
        <span class="font-mono break-all">{newRoot.trim()}</span>. This can take
        hours; you can close this page and come back.
      </p>
      <p class="text-base text-ink-secondary">
        Downloads pause for the duration. Nothing is deleted until its copy has
        been checked, and if anything goes wrong every file is put back and the
        storage root stays where it is.
      </p>
    </div>
    {#snippet footer()}
      <Button variant="secondary" onclick={() => (confirming = null)}>Cancel</Button>
      <Button variant="primary" onclick={migrate}>
        <Icon name="folder" size={14} />
        Move files
      </Button>
    {/snippet}
  </Modal>
{/if}
