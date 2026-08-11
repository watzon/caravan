<script lang="ts">
  import { useI18n } from '../i18n.svelte';
  /**
   * Settings → Downloads → Remote path mappings.
   *
   * Download clients report paths from their own filesystem namespace. This
   * card declares where those same files appear to the host running Caravan.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { RemotePathMapping, RemotePathMappingInput } from '../api/types';
  import { pushToast } from '../state/toast.svelte';
  import Button from './Button.svelte';
  import EmptyState from './EmptyState.svelte';
  import Field from './Field.svelte';
  import LoadError from './LoadError.svelte';
  import Modal from './Modal.svelte';
  import SettingsCard from './SettingsCard.svelte';
  import Skeleton from './Skeleton.svelte';
  import TextInput from './TextInput.svelte';

  let mappings = $state<RemotePathMapping[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let deletingID = $state<number | null>(null);
  let confirmingRemove = $state<RemotePathMapping | null>(null);

  /** null closes the form; 0 adds a mapping; any other value edits that row. */
  let editingID = $state<number | null>(null);
  let remotePath = $state('');
  let localPath = $state('');
  let initialDraft = $state('');
  let formError = $state<string | null>(null);
  let saving = $state(false);

  function draftSnapshot(): string {
    return JSON.stringify({ remotePath, localPath });
  }

  function canonicalizeRoot(value: string): string {
    const trimmed = value.trim();
    if (trimmed === '') return '';
    // Keep filesystem roots intact while making all other mappings compare
    // consistently, regardless of an incidental trailing slash or backslash.
    if (/^[\\/]+$/.test(trimmed)) return trimmed[0]!;
    if (/^[A-Za-z]:[\\/]$/.test(trimmed)) return trimmed;
    return trimmed.replace(/[\\/]+$/, '');
  }

  function validationIssue(): string | null {
    if (!remotePath.trim()) return t('component.remotePathMappings.remotePathRequired');
    if (!localPath.trim()) return t('component.remotePathMappings.localPathRequired');
    return null;
  }

  let isDirty = $derived(editingID !== null && draftSnapshot() !== initialDraft);
  let validationError = $derived(validationIssue());
  let rows = $derived(mappings ?? []);

  async function load() {
    loading = true;
    try {
      mappings = await api.listRemotePathMappings();
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  function openAdd() {
    editingID = 0;
    remotePath = '';
    localPath = '';
    formError = null;
    initialDraft = draftSnapshot();
  }

  function openEdit(mapping: RemotePathMapping) {
    editingID = mapping.id;
    remotePath = mapping.remote_path;
    localPath = mapping.local_path;
    formError = null;
    initialDraft = draftSnapshot();
  }

  function closeForm() {
    editingID = null;
    formError = null;
  }

  function formBody(): RemotePathMappingInput {
    return {
      remote_path: canonicalizeRoot(remotePath),
      local_path: canonicalizeRoot(localPath),
    };
  }

  function validate(): boolean {
    formError = validationError;
    return validationError === null;
  }

  async function save() {
    if (saving || !isDirty || !validate()) return;
    const body = formBody();

    saving = true;
    try {
      if (editingID === 0) {
        const added = await api.addRemotePathMapping(body);
        mappings = [...(mappings ?? []), added];
        pushToast(t('component.remotePathMappings.added'), 'success');
      } else if (editingID !== null) {
        const updated = await api.updateRemotePathMapping(editingID, body);
        mappings = (mappings ?? []).map((mapping) => (mapping.id === updated.id ? updated : mapping));
        pushToast(t('component.remotePathMappings.saved'), 'success');
      }
      closeForm();
    } catch (err) {
      formError = errorText(err);
    } finally {
      saving = false;
    }
  }

  async function remove() {
    const mapping = confirmingRemove;
    if (!mapping) return;

    deletingID = mapping.id;
    try {
      await api.deleteRemotePathMapping(mapping.id);
      mappings = (mappings ?? []).filter((row) => row.id !== mapping.id);
      confirmingRemove = null;
      pushToast(t('component.remotePathMappings.removed'), 'neutral');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      deletingID = null;
    }
  }

  const { t, tp } = useI18n();
</script>

<SettingsCard
  title={t('component.remotePathMappings.remotePathMappings')}
  description={t('component.remotePathMappings.translatePathsReportedByExternalDownloadClientsToPathsOnTheHostRunningCaravan')}>
  {#snippet action()}
    <Button variant="secondary" size="sm" onclick={load}>{t('component.remotePathMappings.refresh')}</Button>
    <Button variant="primary" size="sm" onclick={openAdd}>{t('component.remotePathMappings.addMapping')}</Button>
  {/snippet}

  <p class="mb-4 text-sm text-ink-secondary">{t('component.remotePathMappings.caravanUsesTheLongestMatchingRemotePrefixLocalPathsAreResolvedOnTheHostRunningCaravanNotOnTheDownloadClient')}</p>

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && mappings === null}
    <div class="flex flex-col gap-2">
      {#each Array.from({ length: 2 }) as _, i (i)}
        <Skeleton class="h-14 w-full rounded-md" />
      {/each}
    </div>
  {:else if rows.length === 0}
    <EmptyState
      icon="link"
      title={t('component.remotePathMappings.noRemotePathMappings')}
      message={t('component.remotePathMappings.addOneWhenADownloadClientReportsAFilesystemPathThatDiffersFromCaravanSHost')}>
      {#snippet action()}
        <Button variant="primary" onclick={openAdd}>{t('component.remotePathMappings.addMapping')}</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <ul class="flex flex-col gap-2">
      {#each rows as mapping (mapping.id)}
        <li class="flex flex-wrap items-center gap-3 rounded-md border border-border bg-surface px-3 py-3">
          <div class="min-w-0 flex-1">
            <p class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 font-mono text-sm text-ink">
              <span class="break-all">{mapping.remote_path}</span>
              <span aria-hidden="true" class="text-ink-muted">→</span>
              <span class="sr-only">{t('component.remotePathMappings.mapsTo')}</span>
              <span class="break-all">{mapping.local_path}</span>
            </p>
            <p class="mt-1 text-sm text-ink-secondary">{t('component.remotePathMappings.remoteClientPathToCaravanHostPath')}</p>
            {#if mapping.match_count === 0}
              <p class="mt-1 text-sm text-ink-muted">{t('component.remotePathMappings.noImportsOrEventsHaveMatchedThisMappingYet')}</p>
            {:else}
              <p class="mt-1 text-sm text-ink-muted">
                {mapping.match_count} matched import{mapping.match_count === 1 ? '' : 's'} or event{mapping.match_count === 1 ? '' : 's'}.
                {#if mapping.last_matched_at}
                  Last match: {mapping.last_matched_at}.
                {/if}
              </p>
            {/if}
          </div>
          <div class="flex shrink-0 items-center gap-2">
            <Button variant="ghost" size="sm" onclick={() => openEdit(mapping)}>{t('component.remotePathMappings.edit')}</Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={deletingID === mapping.id}
              onclick={() => (confirmingRemove = mapping)}>
              <span class="sr-only">{t('component.remotePathMappings.removeMapping', { path: mapping.remote_path })}</span>
              <span aria-hidden="true">{t('component.remotePathMappings.remove')}</span>
            </Button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</SettingsCard>

{#if editingID !== null}
  <Modal
    title={editingID === 0 ? t('component.remotePathMappings.addModal') : t('component.remotePathMappings.editModal')}
    width="max-w-xl"
    dirty={isDirty}
    onclose={closeForm}>
    <form
      class="flex flex-col gap-4 p-4"
      onsubmit={(event) => {
        event.preventDefault();
        void save();
      }}>
      <Field
        label={t('component.remotePathMappings.remotePath')}
        for="remote-path"
        help={t('component.remotePathMappings.thePathReportedByTheDownloadClientForExampleDownloads')}>
        <TextInput id="remote-path" bind:value={remotePath} mono autofocus placeholder={t('component.remotePathMappings.downloads')} />
      </Field>
      <Field
        label={t('component.remotePathMappings.localPath')}
        for="local-path"
        help={t('component.remotePathMappings.theMatchingPathOnTheHostRunningCaravanForExampleMntDownloads')}>
        <TextInput id="local-path" bind:value={localPath} mono placeholder={t('component.remotePathMappings.mntDownloads')} />
      </Field>

      {#if formError || (isDirty && validationError)}
        <p class="text-sm text-danger" role="alert">{formError ?? validationError}</p>
      {/if}
    </form>

    {#snippet footer()}
      <div class="flex w-full flex-wrap justify-end gap-2">
        <Button variant="ghost" onclick={closeForm} disabled={saving}>{t('component.remotePathMappings.cancel')}</Button>
        <Button
          variant="primary"
          disabled={saving || !isDirty || validationError !== null}
          title={!isDirty ? t('component.remotePathMappings.noChangesToSave') : validationError ?? undefined}
          onclick={save}>
          {saving ? t('component.remotePathMappings.saving') : !isDirty ? t('component.remotePathMappings.noChanges') : validationError ? t('component.remotePathMappings.fixErrors') : t('component.remotePathMappings.save')}
        </Button>
      </div>
    {/snippet}
  </Modal>
{/if}

{#if confirmingRemove}
  {@const target = confirmingRemove}
  <Modal title={t('component.remotePathMappings.removeRemotePathMapping')} width="max-w-lg" onclose={() => (confirmingRemove = null)}>
    <div class="flex flex-col gap-3 p-4">
      <p class="font-mono text-sm text-ink">{target.remote_path} → {target.local_path}</p>
      <p class="text-base text-ink-secondary">
        {t('component.remotePathMappings.caravanWillStopTranslatingThisRemotePathExistingDownloadsAndFilesAreNotChanged')}
      </p>
    </div>

    {#snippet footer()}
      <div class="flex w-full flex-wrap justify-end gap-2">
        <Button variant="ghost" onclick={() => (confirmingRemove = null)}>{t('component.remotePathMappings.cancel')}</Button>
        <Button variant="danger" disabled={deletingID === target.id} onclick={remove}>
          {deletingID === target.id ? 'Removing…' : 'Remove'}
        </Button>
      </div>
    {/snippet}
  </Modal>
{/if}
