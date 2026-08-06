<script lang="ts">
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
    if (!remotePath.trim()) return 'Remote path is required.';
    if (!localPath.trim()) return 'Local path is required.';
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
        pushToast('Remote path mapping added.', 'success');
      } else if (editingID !== null) {
        const updated = await api.updateRemotePathMapping(editingID, body);
        mappings = (mappings ?? []).map((mapping) => (mapping.id === updated.id ? updated : mapping));
        pushToast('Remote path mapping saved.', 'success');
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
      pushToast('Remote path mapping removed.', 'neutral');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      deletingID = null;
    }
  }
</script>

<SettingsCard
  title="Remote path mappings"
  description="Translate paths reported by external download clients to paths on the host running Caravan.">
  {#snippet action()}
    <Button variant="secondary" size="sm" onclick={load}>Refresh</Button>
    <Button variant="primary" size="sm" onclick={openAdd}>Add mapping</Button>
  {/snippet}

  <p class="mb-4 text-sm text-ink-secondary">Caravan uses the longest matching remote prefix. Local paths are resolved on the host running Caravan, not on the download client.</p>

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
      title="No remote path mappings"
      message="Add one when a download client reports a filesystem path that differs from Caravan's host.">
      {#snippet action()}
        <Button variant="primary" onclick={openAdd}>Add mapping</Button>
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
              <span class="sr-only">maps to</span>
              <span class="break-all">{mapping.local_path}</span>
            </p>
            <p class="mt-1 text-sm text-ink-secondary">Remote client path to Caravan host path</p>
            {#if mapping.match_count === 0}
              <p class="mt-1 text-sm text-ink-muted">No imports or events have matched this mapping yet.</p>
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
            <Button variant="ghost" size="sm" onclick={() => openEdit(mapping)}>Edit</Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={deletingID === mapping.id}
              onclick={() => (confirmingRemove = mapping)}>
              <span class="sr-only">Remove {mapping.remote_path} mapping</span>
              <span aria-hidden="true">Remove</span>
            </Button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</SettingsCard>

{#if editingID !== null}
  <Modal
    title={editingID === 0 ? 'Add remote path mapping' : 'Edit remote path mapping'}
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
        label="Remote path"
        for="remote-path"
        help="The path reported by the download client, for example /downloads.">
        <TextInput id="remote-path" bind:value={remotePath} mono autofocus placeholder="/downloads" />
      </Field>
      <Field
        label="Local path"
        for="local-path"
        help="The matching path on the host running Caravan, for example /mnt/downloads.">
        <TextInput id="local-path" bind:value={localPath} mono placeholder="/mnt/downloads" />
      </Field>

      {#if formError || (isDirty && validationError)}
        <p class="text-sm text-danger" role="alert">{formError ?? validationError}</p>
      {/if}
    </form>

    {#snippet footer()}
      <div class="flex w-full flex-wrap justify-end gap-2">
        <Button variant="ghost" onclick={closeForm} disabled={saving}>Cancel</Button>
        <Button
          variant="primary"
          disabled={saving || !isDirty || validationError !== null}
          title={!isDirty ? 'No changes to save' : validationError ?? undefined}
          onclick={save}>
          {saving ? 'Saving…' : !isDirty ? 'No changes' : validationError ? 'Fix errors' : 'Save'}
        </Button>
      </div>
    {/snippet}
  </Modal>
{/if}

{#if confirmingRemove}
  {@const target = confirmingRemove}
  <Modal title="Remove remote path mapping" width="max-w-lg" onclose={() => (confirmingRemove = null)}>
    <div class="flex flex-col gap-3 p-4">
      <p class="font-mono text-sm text-ink">{target.remote_path} → {target.local_path}</p>
      <p class="text-base text-ink-secondary">
        Caravan will stop translating this remote path. Existing downloads and files are not changed.
      </p>
    </div>

    {#snippet footer()}
      <div class="flex w-full flex-wrap justify-end gap-2">
        <Button variant="ghost" onclick={() => (confirmingRemove = null)}>Cancel</Button>
        <Button variant="danger" disabled={deletingID === target.id} onclick={remove}>
          {deletingID === target.id ? 'Removing…' : 'Remove'}
        </Button>
      </div>
    {/snippet}
  </Modal>
{/if}
