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
  import { onDestroy, onMount } from 'svelte';
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
  let confirming = $state<'repoint' | 'migrate' | null>(null);
  let warnings = $state<string[]>([]);
  let restartRequired = $state(false);
  let migration = $state<StorageMigration | null>(null);

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
