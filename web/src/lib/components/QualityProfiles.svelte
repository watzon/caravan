<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError, api, errorText } from '../api/client';
  import type { Quality, QualityProfile, QualityProfileInput } from '../api/types';
  import { QUALITY_LADDER } from '../api/types';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import EmptyState from './EmptyState.svelte';
  import Field from './Field.svelte';
  import LoadError from './LoadError.svelte';
  import Modal from './Modal.svelte';
  import Skeleton from './Skeleton.svelte';
  import TextInput from './TextInput.svelte';
  import Toggle from './Toggle.svelte';
  import { pushToast } from '../state/toast.svelte';

  let profiles = $state<QualityProfile[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let formOpen = $state(false);
  let editing = $state<QualityProfile | null>(null);
  let deleting = $state<QualityProfile | null>(null);
  let deletingBusy = $state(false);
  let deleteError = $state<string | null>(null);
  let saving = $state(false);

  let name = $state('');
  let items = $state<Quality[]>([]);
  let cutoff = $state<Quality>('1080p');
  let upgradeAllowed = $state(true);
  let nameError = $state<string | null>(null);
  let itemsError = $state<string | null>(null);
  let cutoffError = $state<string | null>(null);

  async function load() {
    loading = true;
    try {
      profiles = await api.listQualityProfiles();
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  function resetForm(profile: QualityProfile | null) {
    editing = profile;
    name = profile?.name ?? '';
    items = profile ? QUALITY_LADDER.filter((item) => profile.items.includes(item)) : ['1080p'];
    cutoff = profile?.cutoff ?? '1080p';
    upgradeAllowed = profile?.upgrade_allowed ?? true;
    nameError = null;
    itemsError = null;
    cutoffError = null;
  }

  function openCreate() {
    resetForm(null);
    formOpen = true;
  }

  function openEdit(profile: QualityProfile) {
    resetForm(profile);
    formOpen = true;
  }

  function toggleItem(item: Quality) {
    if (items.includes(item)) {
      items = items.filter((candidate) => candidate !== item);
      if (cutoff === item) cutoff = items[0] ?? '1080p';
      return;
    }
    const next = QUALITY_LADDER.filter((candidate) => candidate === item || items.includes(candidate));
    items = next;
    if (!next.includes(cutoff)) cutoff = next[0]!;
  }

  function validate(): QualityProfileInput | null {
    nameError = name.trim() === '' ? 'Enter a profile name.' : null;
    itemsError = items.length === 0 ? 'Select at least one quality.' : null;
    cutoffError = !items.includes(cutoff) ? 'Choose a cutoff from the selected qualities.' : null;
    if (nameError || itemsError || cutoffError) return null;
    return { name: name.trim(), items, cutoff, upgrade_allowed: upgradeAllowed };
  }

  async function save() {
    const body = validate();
    if (!body) return;
    saving = true;
    try {
      const profile = editing
        ? await api.updateQualityProfile(editing.id, body)
        : await api.addQualityProfile(body);
      profiles = editing
        ? (profiles ?? []).map((current) => current.id === profile.id ? profile : current)
        : [...(profiles ?? []), profile];
      formOpen = false;
      pushToast(editing ? 'Quality profile updated.' : 'Quality profile created.', 'success');
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        nameError = 'a profile with that name already exists';
      } else {
        pushToast(errorText(err), 'danger');
      }
    } finally {
      saving = false;
    }
  }

  async function confirmDelete() {
    const profile = deleting;
    if (!profile) return;
    deletingBusy = true;
    deleteError = null;
    try {
      await api.deleteQualityProfile(profile.id);
      profiles = (profiles ?? []).filter((current) => current.id !== profile.id);
      deleting = null;
      pushToast('Quality profile deleted.', 'success');
    } catch (err) {
      deleteError = errorText(err);
    } finally {
      deletingBusy = false;
    }
  }
</script>

<div class="flex max-w-5xl flex-col gap-5">
  <div class="flex flex-wrap items-center gap-3">
    <p class="flex-1 text-base text-ink-secondary">Define the qualities Caravan may accept and the point where it should keep looking for an upgrade.</p>
    <Button variant="primary" onclick={openCreate}>New profile</Button>
  </div>

  {#if error && profiles === null}
    <LoadError message={error} onretry={load} />
  {:else if loading && profiles === null}
    <div class="flex flex-col gap-2">{#each Array.from({ length: 3 }) as _, i (i)}<Skeleton class="h-20 w-full rounded-md" />{/each}</div>
  {:else if (profiles ?? []).length === 0}
    <EmptyState icon="settings" title="No quality profiles" message="Create a profile to define which releases Caravan can accept." />
  {:else}
    <ul class="overflow-hidden rounded-md border border-border bg-surface">
      {#each profiles ?? [] as profile (profile.id)}
        <li class="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3 last:border-b-0">
          <div class="min-w-36 flex-1">
            <p class="font-medium text-ink">{profile.name}</p>
            <p class="mt-0.5 text-sm text-ink-secondary">{profile.upgrade_allowed ? 'Upgrades allowed' : 'Upgrades disabled'}</p>
          </div>
          <Badge tone="warning" mono title="Quality cutoff">{profile.cutoff}</Badge>
          <div class="flex flex-wrap gap-1" aria-label={`${profile.name} allowed qualities`}>
            {#each QUALITY_LADDER.filter((item) => profile.items.includes(item)) as item (item)}
              <Badge mono tone="neutral">{item}</Badge>
            {/each}
          </div>
          <div class="ml-auto flex items-center gap-2">
            <Button variant="secondary" size="sm" onclick={() => openEdit(profile)}>Edit</Button>
            <Button variant="ghost" size="sm" onclick={() => { deleting = profile; deleteError = null; }}>Delete</Button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</div>

{#if formOpen}
  <Modal title={editing ? 'Edit quality profile' : 'New quality profile'} width="max-w-lg" onclose={() => (formOpen = false)}>
    <form class="flex flex-col gap-5 p-4" onsubmit={(event) => { event.preventDefault(); save(); }}>
      <Field label="Name" for="quality-profile-name" error={nameError}>
        <TextInput id="quality-profile-name" bind:value={name} autofocus />
      </Field>

      <Field label="Allowed qualities" error={itemsError} help="Qualities stay in the fixed best-first order.">
        <div class="grid grid-cols-2 gap-2" role="group" aria-label="Allowed qualities">
          {#each QUALITY_LADDER as item (item)}
            <label class="flex items-center gap-2 rounded-sm border border-border bg-raised px-3 py-2 text-base text-ink">
              <input type="checkbox" checked={items.includes(item)} onchange={() => toggleItem(item)} class="size-4 accent-accent" />
              <span class="font-mono text-sm">{item}</span>
            </label>
          {/each}
        </div>
        {#if items.length > 0}
          <div class="flex flex-wrap gap-1" aria-label="Selected quality order">
            {#each items as item (item)}<Badge mono tone="neutral">{item}</Badge>{/each}
          </div>
        {/if}
      </Field>

      <Field label="Cutoff" for="quality-profile-cutoff" error={cutoffError}>
        <select id="quality-profile-cutoff" bind:value={cutoff} class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink focus:border-accent focus:outline-none">
          {#each items as item (item)}<option value={item}>{item}</option>{/each}
        </select>
      </Field>

      <Toggle checked={upgradeAllowed} label="Allow upgrades above the cutoff" onchange={(next) => (upgradeAllowed = next)} />
    </form>
    {#snippet footer()}
      <Button variant="ghost" onclick={() => (formOpen = false)}>Cancel</Button>
      <Button variant="primary" disabled={saving} onclick={save}>{saving ? 'Saving...' : editing ? 'Save changes' : 'Create profile'}</Button>
    {/snippet}
  </Modal>
{/if}

{#if deleting}
  {@const profile = deleting}
  <Modal title="Delete quality profile" width="max-w-md" onclose={() => (deleting = null)}>
    <div class="flex flex-col gap-3 p-4">
      <p class="text-base text-ink-secondary">Delete <span class="font-medium text-ink">{profile.name}</span>? This cannot be undone.</p>
      {#if deleteError}<p class="text-sm text-danger">{deleteError}</p>{/if}
    </div>
    {#snippet footer()}
      <Button variant="ghost" disabled={deletingBusy} onclick={() => (deleting = null)}>Cancel</Button>
      <Button variant="danger" disabled={deletingBusy} onclick={confirmDelete}>{deletingBusy ? 'Deleting...' : 'Delete'}</Button>
    {/snippet}
  </Modal>
{/if}
