<script lang="ts">
  import { useI18n } from '../i18n.svelte';
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
  import DirectoryPicker from './DirectoryPicker.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';
  import Modal from './Modal.svelte';
  import ProgressBar from './ProgressBar.svelte';
  import TextInput from './TextInput.svelte';
  import DatabaseSettings from './DatabaseSettings.svelte';

  interface Props {
    settings: Settings;
    saving?: boolean;
    onsave: (patch: Settings, note: string) => Promise<boolean>;
  }

  let { settings, saving = false, onsave }: Props = $props();

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
      return t('component.storageSettings.retentionInvalid');
    }
    const formats: Array<[string, string, string[], string[]]> = [
      [t('component.storageSettings.movieFolderLabel'), movieFolderFormat, ['title', 'year'], ['title']],
      [t('component.storageSettings.movieFileLabel'), movieFileFormat, ['title', 'year', 'edition'], ['title']],
      [t('component.storageSettings.seriesFolderLabel'), seriesFolderFormat, ['title', 'year'], ['title']],
      [t('component.storageSettings.seasonFolderLabel'), seasonFolderFormat, ['season', 'season:02'], ['season']],
      [t('component.storageSettings.episodeFileLabel'), episodeFileFormat, ['series', 'year', 'episode', 'title'], ['series', 'episode']],
    ];
    for (const [label, format, allowed, required] of formats) {
      const tokens = [...format.matchAll(/\{([^}]+)\}/g)].map((match) => match[1]);
      if (!format || tokens.some((token) => !allowed.includes(token))) return t('component.storageSettings.invalidToken', { label });
      if (required.some((token) => token === 'season'
        ? !tokens.includes('season') && !tokens.includes('season:02')
        : !tokens.includes(token))) return t('component.storageSettings.requiredToken', { label });
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
      const saved = await onsave(
        {
          [namingKeys.recycle]: String(recycleRetentionDays),
          [namingKeys.movieFolder]: movieFolderFormat,
          [namingKeys.movieFile]: movieFileFormat,
          [namingKeys.seriesFolder]: seriesFolderFormat,
          [namingKeys.seasonFolder]: seasonFolderFormat,
          [namingKeys.episodeFile]: episodeFileFormat,
        },
        t('component.storageSettings.namingSaved'),
      );
      if (saved) savedNaming = namingSnapshot();
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
      pushToast(t('component.storageSettings.repointed'), 'success');
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
      pushToast(t('component.storageSettings.migrationQueued'), 'success');
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
        t('component.storageSettings.scanFinished', {
          files: summary.media_files,
          unmatched: summary.unmatched,
        }),
        summary.unmatched > 0 ? 'warning' : 'success',
      );
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      scanning = false;
    }
  }

  const { t, tp } = useI18n();
</script>

<section class="flex flex-col gap-6">
  <Field
    label={t('component.storageSettings.currentStorageRoot')}
    for="settings-storage-root-current"

    help={t('component.storageSettings.everyPathInTheDatabaseIsRelativeToThisFolderThatIsWhatMakesRePointingInstantAndARescanEnoughToRebuildTheLibrary')}>
    <TextInput id="settings-storage-root-current" value={currentRoot} mono readonly />
  </Field>
  <div class="flex flex-col gap-4 rounded-md border border-border bg-surface p-4">
    <div>
      <h3 class="text-sm font-semibold">{t('component.storageSettings.recycleAndLibraryNaming')}</h3>
      <p class="mt-1 text-sm text-muted">
        {t('component.storageSettings.deletedCaravanFilesCanBeRetainedUnder')} <code>recycle/&lt;UTC batch&gt;/</code>{t('component.storageSettings.namingChangesApplyToNewImportsOnly')}
      </p>
    </div>
    <Field
      label={t('component.storageSettings.recycleRetentionDays')}
      for="settings-recycle-retention"
      help={t('component.storageSettings.0PermanentlyDeletesFutureCaravanOwnedMediaPostersAndNfoFilesUse1To3650DaysToRetainThem')}
    >
      <TextInput id="settings-recycle-retention" type="number" min="0" max="3650" bind:value={recycleRetentionDays} />
    </Field>
    {#if permanentDeletion}
      <Banner
        tone="danger"
        icon="warning"
        title={t('component.storageSettings.permanentDeletion')}
        message={t('component.storageSettings.with0DaysOfRetentionFutureCaravanDeletionsPermanentlyRemoveCaravanOwnedMediaPostersAndNfoFilesInsteadOfMovingThemToRecycle')} />
    {/if}
    <Field label={t('component.storageSettings.movieFolderFormat')} for="settings-movie-folder-format" help={t('component.storageSettings.tokensMovie')}>
      <TextInput id="settings-movie-folder-format" bind:value={movieFolderFormat} mono />
    </Field>
    <Field label={t('component.storageSettings.movieFileFormat')} for="settings-movie-file-format" help={t('component.storageSettings.tokensMovieFile')}>
      <TextInput id="settings-movie-file-format" bind:value={movieFileFormat} mono />
    </Field>
    <Field label={t('component.storageSettings.seriesFolderFormat')} for="settings-series-folder-format" help={t('component.storageSettings.tokensMovie')}>
      <TextInput id="settings-series-folder-format" bind:value={seriesFolderFormat} mono />
    </Field>
    <Field label={t('component.storageSettings.seasonFolderFormat')} for="settings-season-folder-format" help={t('component.storageSettings.tokensSeason')}>
      <TextInput id="settings-season-folder-format" bind:value={seasonFolderFormat} mono />
    </Field>
    <Field
      label={t('component.storageSettings.episodeFileFormat')}
      for="settings-episode-file-format"
      help={t('component.storageSettings.tokensEpisode')}
    >
      <TextInput id="settings-episode-file-format" bind:value={episodeFileFormat} mono />
    </Field>
    <div class="rounded bg-base px-3 py-2 text-sm text-muted">
      <p>{t('component.storageSettings.moviePreview')} <code>{preview(movieFolderFormat, { title: 'Big Buck Bunny', year: ' (2008)' })}/{moviePreview}</code></p>
      <p>{t('component.storageSettings.episodePreview')} <code>{preview(seriesFolderFormat, { title: 'Planet Earth II', year: ' (2016)' })}/{preview(seasonFolderFormat, { season: '1', 'season:02': '01' })}/{episodePreview}</code></p>
    </div>
    <div>
      <Button
        variant={permanentDeletion ? 'danger' : 'primary'}
        onclick={requestNamingSave}
        disabled={saving || namingBusy || !namingChanged || namingInvalid}
      >
        {saving || namingBusy ? t('component.storageSettings.saving') : t('component.storageSettings.saveNaming')}
      </Button>
    </div>
  </div>
  <DatabaseSettings />

  {#if portable}
    <Banner
      tone="info"
      icon="warning"
      title={t('component.storageSettings.thisIsAPortableDrive')}
      message={t('component.storageSettings.portableDriveHelp')} />
  {:else}
    <Field
      label={t('component.storageSettings.newStorageRoot')}
      for="settings-storage-root"
      help={t('component.storageSettings.anAbsolutePathThatAlreadyExistsOnThisMachine')}>
      <DirectoryPicker id="settings-storage-root" bind:value={newRoot} placeholder={t('component.storageSettings.data')} />
    </Field>
  {/if}

  <div class="flex flex-wrap gap-2">
    {#if !portable}
      <Button variant="primary" disabled={!canAct} onclick={() => (confirming = 'repoint')}>
        <Icon name="check" size={14} />
        {t('component.storageSettings.rePoint')}
      </Button>
      <Button variant="secondary" disabled={!canAct} onclick={() => (confirming = 'migrate')}>
        <Icon name="folder" size={14} />
        {t('component.storageSettings.moveFilesHere')}
      </Button>
    {/if}
    <Button variant="secondary" disabled={scanning || running} onclick={rescan}>
      <Icon name="refresh" size={14} />
      {scanning ? t('component.storageSettings.scanning') : t('component.storageSettings.rescanLibrary')}
    </Button>
  </div>

  {#each warnings as warning (warning)}
    <Banner tone="warning" icon="warning" title={t('component.storageSettings.rePointedAnyway')} message={warning} />
  {/each}

  {#if restartRequired}
    <Banner
      tone="warning"
      icon="warning"
      title={t('component.storageSettings.restartToMoveTheDownloadQueue')}
      message={t('component.storageSettings.theLibraryArtworkAndMediaServerAreAlreadyUsingTheNewRootTheDownloadEngineKeepsWritingUnderTheOldOneUntilCaravanRestarts')} />
  {/if}

  {#if migration}
    <div class="flex flex-col gap-3 rounded-md border border-border bg-surface p-4">
      <div class="flex items-baseline justify-between gap-3">
        <p class="text-base font-semibold text-ink">
          {running ? t('component.storageSettings.movingFiles') : t('component.storageSettings.lastMigration')}
        </p>
        <p class="font-mono text-xs text-ink-secondary">
          {t('component.storageSettings.migrationFileProgress', {
            done: migration.files_done,
            total: migration.files_total,
          })}
          · {t('component.storageSettings.migrationBytesProgress', {
            done: formatBytes(migration.bytes_done),
            total: formatBytes(migration.bytes_total),
          })}
        </p>
      </div>
      <ProgressBar
        value={progress}
        tone={migration.status === 'done'
          ? 'success'
          : migration.status === 'queued' || migration.status === 'running'
            ? 'accent'
            : 'danger'}
        label={t('component.storageSettings.storageMigrationProgress')} />
      <p class="font-mono text-xs break-all text-ink-secondary">
        {migration.source_root} → {migration.target_root}
      </p>

      {#if migration.status === 'queued'}
        <Banner tone="info" icon="warning" message={t('component.storageSettings.queuedTheMoveStartsAsSoonAsTheWorkerPicksItUp')} />
      {:else if migration.status === 'running'}
        <Banner
          tone="info"
          icon="warning"
          message={t('component.storageSettings.downloadsArePausedWhileTheFilesMoveTheStorageRootChangesOnlyOnceEveryFileHasArrivedAndBeenChecked')} />
      {:else if migration.status === 'done'}
        <Banner
          tone="success"
          icon="check"
          title={t('component.storageSettings.migrationFinished')}
          message={t('component.storageSettings.everyFileArrivedAtTheNewRootAndTheStorageRootNowPointsAtItRestartCaravanToResumeDownloadsThere')} />
      {:else if migration.status === 'rolled_back'}
        <Banner
          tone="warning"
          icon="warning"
          title={t('component.storageSettings.migrationRolledBack')}
          message={t('component.storageSettings.migrationRolledBackMessage', {
            error: migration.error,
            sourceRoot: migration.source_root,
          })} />
      {:else}
        <Banner
          tone="danger"
          icon="warning"
          title={t('component.storageSettings.migrationFailedAndCouldNotBeUndone')}
          message={t('component.storageSettings.migrationFailedMessage', { error: migration.error })} />
      {/if}
    </div>
  {/if}
</section>

{#if confirming === 'permanent-delete'}
  <Modal title={t('component.storageSettings.usePermanentDeletion')} width="max-w-md" onclose={() => (confirming = null)}>
    <div class="flex flex-col gap-3 px-4 py-4">
      <p class="text-base text-ink-secondary">
        {t('component.storageSettings.permanentDeletionMessage')}
      </p>
      <p class="text-base text-ink-secondary">
        {t('component.storageSettings.savingThisSettingDoesNotDeleteAnythingNowOrRemoveFilesAlreadyInRecycle')}
      </p>
    </div>
    {#snippet footer()}
      <Button variant="secondary" onclick={() => (confirming = null)}>{t('component.storageSettings.cancel')}</Button>
      <Button variant="danger" onclick={saveNaming}>{t('component.storageSettings.saveWithPermanentDeletion')}</Button>
    {/snippet}
  </Modal>
{/if}

{#if confirming === 'repoint'}
  <Modal title={t('component.storageSettings.rePointTheStorageRoot')} width="max-w-md" onclose={() => (confirming = null)}>
    <div class="flex flex-col gap-3 px-4 py-4">
      <p class="text-base text-ink-secondary">
        {t('component.storageSettings.caravanWillLookForTheLibraryUnder')} <span class="font-mono break-all">{newRoot.trim()}</span>
        {t('component.storageSettings.fromNowOnNoFilesAreMoved')}
      </p>
      <p class="text-base text-ink-secondary">
        {t('component.storageSettings.repointMissingMedia')}
      </p>
    </div>
    {#snippet footer()}
      <Button variant="secondary" onclick={() => (confirming = null)}>{t('component.storageSettings.cancel')}</Button>
      <Button variant="primary" onclick={repoint}>
        <Icon name="check" size={14} />
        {t('component.storageSettings.rePoint')}
      </Button>
    {/snippet}
  </Modal>
{/if}

{#if confirming === 'migrate'}
  <Modal title={t('component.storageSettings.moveTheLibrary')} width="max-w-md" onclose={() => (confirming = null)}>
    <div class="flex flex-col gap-3 px-4 py-4">
      <p class="text-base text-ink-secondary">
        {t('component.storageSettings.caravanWillMoveTheLibraryAndIncompleteFoldersFrom')}
        <span class="font-mono break-all">{currentRoot}</span> {t('component.storageSettings.to')}
        <span class="font-mono break-all">{newRoot.trim()}</span>. {t('component.storageSettings.migrationDuration')}
      </p>
      <p class="text-base text-ink-secondary">
        {t('component.storageSettings.migrationSafety')}
      </p>
    </div>
    {#snippet footer()}
      <Button variant="secondary" onclick={() => (confirming = null)}>{t('component.storageSettings.cancel')}</Button>
      <Button variant="primary" onclick={migrate}>
        <Icon name="folder" size={14} />
        {t('component.storageSettings.moveFiles')}
      </Button>
    {/snippet}
  </Modal>
{/if}
