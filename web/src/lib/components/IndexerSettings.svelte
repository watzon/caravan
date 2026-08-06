<script lang="ts">
  /**
   * Settings → Indexers (SPEC §5.1, §11 `/indexers`). Caravan ships with none
   * preconfigured, so this screen is where every search source comes from.
   *
   * The test button is the point of the screen: an indexer that is configured
   * but unreachable must say so here rather than silently returning nothing on
   * every search (SPEC §13).
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Indexer, IndexerCategory, IndexerInput, IndexerType } from '../api/types';
  import {
    INDEXER_TYPES,
    formatCategories,
    parseCategories,
    validateIndexer,
  } from '../indexer';
  import { pushToast } from '../state/toast.svelte';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import CategoryPicker from './CategoryPicker.svelte';
  import EmptyState from './EmptyState.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';
  import LoadError from './LoadError.svelte';
  import Modal from './Modal.svelte';
  import Skeleton from './Skeleton.svelte';
  import TextInput from './TextInput.svelte';
  import Toggle from './Toggle.svelte';

  /** The result of the last test per indexer id, so the row can say what happened. */
  type TestResult = { ok: boolean; message: string };

  let indexers = $state<Indexer[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let tests = $state<Record<number, TestResult>>({});
  let testingID = $state<number | null>(null);
  let busyID = $state<number | null>(null);
  let confirmingRemove = $state<Indexer | null>(null);

  /** null = the form is closed; 0 = adding; otherwise the id being edited. */
  let editingID = $state<number | null>(null);
  let saving = $state(false);
  let formError = $state<string | null>(null);

  let name = $state('');
  let type = $state<IndexerType>('torznab');
  let url = $state('');
  let apiKey = $state('');
  let hasAPIKey = $state(false);
  let clearAPIKey = $state(false);
  let categories = $state('');
  let priority = $state('25');
  let enabled = $state(true);

  /**
   * A form's baseline is captured only when it opens. This keeps category-cap
   * loading from looking like an edit, while treating the write-only key's
   * explicit Clear action as a real change.
   */
  let initialDraft = $state('');

  function draftSnapshot(): string {
    return JSON.stringify({
      name,
      type,
      url,
      apiKey,
      clearAPIKey,
      categories,
      priority,
      selectedCategories,
      enabled,
    });
  }

  let isDirty = $derived(editingID !== null && draftSnapshot() !== initialDraft);
  let priorityError = $derived.by(() => {
    const value = Number(priority.trim());
    return priority.trim() === '' || !Number.isInteger(value) || value < 0
      ? 'Priority must be a whole number of zero or greater.'
      : null;
  });
  let validationError = $derived(validateIndexer({ name, url }) ?? priorityError);
  /**
   * The category tree the indexer advertises (null = not loaded), and the ids
   * picked from it. While the tree is unloaded — or the indexer advertises
   * none — the free-text `categories` field above stays the editing surface,
   * so a save never depends on the indexer being reachable.
   */
  let categoryTree = $state<IndexerCategory[] | null>(null);
  let selectedCategories = $state<number[]>([]);
  let categoriesLoading = $state(false);
  let categoriesError = $state<string | null>(null);
  let categoriesAbort: AbortController | null = null;

  let treeUsable = $derived(categoryTree !== null && categoryTree.length > 0);

  function resetCategoryPicker() {
    categoriesAbort?.abort();
    categoriesAbort = null;
    categoryTree = null;
    selectedCategories = [];
    categoriesLoading = false;
    categoriesError = null;
  }

  async function loadCategories() {
    categoriesAbort?.abort();
    const ac = new AbortController();
    categoriesAbort = ac;
    categoriesLoading = true;
    categoriesError = null;
    // Whatever was typed by hand carries into the picker as the selection.
    if (categoryTree === null) selectedCategories = parseCategories(categories);

    try {
      const tree = await api.indexerCategories(
        { url: url.trim(), api_key: apiKey.trim(), type },
        ac.signal,
      );
      if (categoriesAbort !== ac) return;
      categoryTree = tree;
      if (tree.length === 0) {
        categoriesError = 'This indexer advertises no categories — enter ids by hand.';
      }
    } catch (err) {
      if (categoriesAbort !== ac) return;
      categoriesError = errorText(err);
    } finally {
      if (categoriesAbort === ac) categoriesLoading = false;
    }
  }

  async function load() {
    loading = true;
    try {
      indexers = await api.listIndexers();
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
    formError = null;
    name = '';
    type = 'torznab';
    url = '';
    apiKey = '';
    hasAPIKey = false;
    clearAPIKey = false;
    categories = '';
    priority = '25';
    enabled = true;
    resetCategoryPicker();
    initialDraft = draftSnapshot();
  }

  function openEdit(indexer: Indexer) {
    editingID = indexer.id;
    formError = null;
    name = indexer.name;
    type = indexer.type;
    url = indexer.url;
    apiKey = '';
    hasAPIKey = indexer.has_api_key;
    clearAPIKey = false;
    categories = formatCategories(indexer.categories);
    priority = String(indexer.priority ?? 25);
    enabled = indexer.enabled;
    resetCategoryPicker();
    selectedCategories = indexer.categories;
    // Best effort: a failure leaves the free-text field as the editor.
    void loadCategories();
    initialDraft = draftSnapshot();
  }

  function closeForm() {
    editingID = null;
    formError = null;
    resetCategoryPicker();
  }

  async function save() {
    if (saving || !isDirty) return;
    if (validationError) {
      formError = validationError;
      return;
    }

    const body: IndexerInput = {
      name: name.trim(),
      type,
      url: url.trim(),
      categories: treeUsable ? selectedCategories : parseCategories(categories),
      priority: Number(priority.trim()),
      enabled,
    };
    if (apiKey.trim() !== '' || clearAPIKey) {
      body.api_key = apiKey.trim();
    }

    saving = true;
    try {
      if (editingID === 0) {
        await api.addIndexer(body);
        pushToast(`Added ${body.name}.`, 'success');
      } else if (editingID !== null) {
        await api.updateIndexer(editingID, body);
        pushToast(`Saved ${body.name}.`, 'success');
      }
      closeForm();
      await load();
    } catch (err) {
      formError = errorText(err);
    } finally {
      saving = false;
    }
  }

  async function test(indexer: Indexer) {
    testingID = indexer.id;
    try {
      await api.testIndexer(indexer.id);
      tests = { ...tests, [indexer.id]: { ok: true, message: 'Reachable' } };
    } catch (err) {
      tests = { ...tests, [indexer.id]: { ok: false, message: errorText(err) } };
    } finally {
      testingID = null;
    }
  }

  async function remove() {
    const indexer = confirmingRemove;
    if (!indexer) return;
    busyID = indexer.id;
    try {
      await api.deleteIndexer(indexer.id);
      indexers = (indexers ?? []).filter((i) => i.id !== indexer.id);
      if (editingID === indexer.id) closeForm();
      confirmingRemove = null;
      pushToast(`Removed ${indexer.name}.`, 'neutral');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busyID = null;
    }
  }

  let rows = $derived(indexers ?? []);

  /** The row the open editor belongs to, which is what its Remove acts on. */
  let editingRow = $derived(rows.find((indexer) => indexer.id === editingID) ?? null);
</script>

<section class="flex flex-col gap-4">
  <div class="flex flex-wrap items-center gap-3">
    <div class="ml-auto flex items-center gap-2">
      <Button variant="secondary" onclick={load}>
        <Icon name="refresh" size={14} />
        Refresh
      </Button>
      <Button variant="primary" onclick={openAdd}>
        <Icon name="plus" size={14} />
        Add indexer
      </Button>
    </div>
  </div>

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && indexers === null}
    <div class="flex flex-col gap-2">
      {#each Array.from({ length: 3 }) as _, i (i)}
        <Skeleton class="h-14 w-full rounded-md" />
      {/each}
    </div>
  {:else if rows.length === 0}
    <EmptyState
      icon="link"
      title="No indexers yet"
      message="Add a Torznab or Newznab source — a Jackett or Prowlarr endpoint, or an indexer's own API — and interactive search starts working.">
      {#snippet action()}
        <Button variant="primary" onclick={openAdd}>Add indexer</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <ul class="flex flex-col gap-2">
      {#each rows as indexer (indexer.id)}
        {@const result = tests[indexer.id]}
        <li class="flex flex-wrap items-center gap-3 rounded-md border border-border bg-surface px-3 py-3">
          <span
            class="size-2 shrink-0 rounded-full {indexer.enabled ? 'bg-success' : 'bg-ink-muted'}">
          </span>
          <span class="sr-only">{indexer.enabled ? 'Enabled' : 'Disabled'}</span>

          <div class="min-w-0 flex-1">
            <p class="flex flex-wrap items-center gap-2">
              <span class="truncate text-base font-medium text-ink">{indexer.name}</span>
              <Badge mono tone={indexer.type === 'torznab' ? 'accent' : 'info'}>
                {indexer.type}
              </Badge>
              <Badge mono tone="neutral">Priority {indexer.priority ?? 25}</Badge>
              {#if !indexer.enabled}
                <Badge tone="neutral">Disabled</Badge>
              {/if}
            </p>
            <p class="truncate font-mono text-xs text-ink-muted" title={indexer.url}>
              {indexer.url}
            </p>
            {#if result}
              <p class="mt-1 text-sm {result.ok ? 'text-success' : 'text-danger'}">
                {result.ok ? '✓ ' : '✕ '}{result.message}
              </p>
            {/if}
          </div>

          <div class="flex shrink-0 items-center gap-2">
            <Button
              variant="secondary"
              size="sm"
              disabled={testingID === indexer.id}
              onclick={() => test(indexer)}>
              {testingID === indexer.id ? 'Testing…' : 'Test'}
            </Button>
            <Button variant="ghost" size="sm" onclick={() => openEdit(indexer)}>Edit</Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={busyID === indexer.id}
              onclick={() => (confirmingRemove = indexer)}>
              <Icon name="trash" size={14} />
              <span class="sr-only">Remove {indexer.name}</span>
            </Button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</section>

{#if editingID !== null}
  <Modal
    title={editingID === 0 ? 'Add indexer' : 'Edit indexer'}
    width="max-w-xl"
    dirty={isDirty}
    onclose={closeForm}>
    <form
      class="flex flex-col gap-4 p-4"
      onsubmit={(event) => {
        event.preventDefault();
        void save();
      }}>
      <Field label="Name" for="indexer-name" help="How this source is labelled in the release picker.">
        <TextInput id="indexer-name" bind:value={name} placeholder="Jackett — 1337x" />
      </Field>

      <Field label="Type" help={INDEXER_TYPES.find((t) => t.value === type)?.help ?? ''}>
        <div class="flex gap-2" role="radiogroup" aria-label="Indexer type">
          {#each INDEXER_TYPES as option (option.value)}
            <button
              type="button"
              role="radio"
              aria-checked={type === option.value}
              onclick={() => (type = option.value)}
              class="h-8 rounded-full border px-3 text-sm transition-colors duration-150 ease-out
                     {type === option.value
                ? 'border-accent bg-accent-tint text-accent-text'
                : 'border-border bg-raised text-ink-secondary hover:text-ink'}">
              {option.label}
            </button>
          {/each}
        </div>
      </Field>

      <Field
        label="Base URL"
        for="indexer-url"
        help="The API root, without /api or a query string — for example http://127.0.0.1:9117/api/v2.0/indexers/1337x/results/torznab.">
        <TextInput id="indexer-url" bind:value={url} mono placeholder="http://127.0.0.1:9117/…" />
      </Field>

      <Field
        label="API key"
        for="indexer-key"
        help="Stored in the database, never in caravan.yaml and never logged.">
        <div class="flex flex-col gap-2">
          <TextInput
            id="indexer-key"
            bind:value={apiKey}
            type="password"
            mono
            placeholder="•••••"
            oninput={() => (clearAPIKey = false)} />
          {#if hasAPIKey}
            <p class="text-sm text-ink-secondary">A key is stored. Leave blank to keep it.</p>
            <Button variant="secondary" size="sm" onclick={() => (clearAPIKey = true)}>
              Clear API key
            </Button>
          {/if}
        </div>
      </Field>

      <Field
        label="Priority"
        for="indexer-priority"
        error={priorityError ?? undefined}
        help="Lowest wins. Caravan searches lower-priority sources first, which also breaks otherwise equal release choices.">
        <TextInput
          id="indexer-priority"
          bind:value={priority}
          mono
          aria-invalid={priorityError !== null}
          aria-describedby={priorityError ? 'indexer-priority-error' : undefined}
          placeholder="25" />
      </Field>

      <div data-settings-advanced>
        <Field
          label="Categories"
          for={treeUsable ? undefined : 'indexer-categories'}
          help={treeUsable
            ? 'Caravan searches only the selected categories. Nothing selected searches everything.'
            : 'Load the list from the indexer, or enter category ids by hand. Empty searches everything.'}>
          <div class="flex flex-col gap-2">
            {#if treeUsable && categoryTree}
              <CategoryPicker
                tree={categoryTree}
                selected={selectedCategories}
                onchange={(ids) => (selectedCategories = ids)} />
            {:else}
              <TextInput id="indexer-categories" bind:value={categories} mono placeholder="2000, 5000" />
            {/if}
            <div class="flex flex-wrap items-center gap-2">
              <Button
                variant="secondary"
                size="sm"
                disabled={categoriesLoading || url.trim() === ''}
                onclick={loadCategories}>
                <Icon name="refresh" size={14} />
                {categoriesLoading
                  ? 'Loading…'
                  : treeUsable
                    ? 'Reload from indexer'
                    : 'Load from indexer'}
              </Button>
            </div>
          </div>
        </Field>
      </div>
      {#if categoriesError}
        <p class="text-sm text-danger">{categoriesError}</p>
      {/if}

      <Toggle
        checked={enabled}
        label="Enabled"
        onchange={(next) => (enabled = next)} />

      {#if formError || (isDirty && validationError)}
        <p class="text-sm text-danger">{formError ?? validationError}</p>
      {/if}
    </form>

    {#snippet footer()}
      {#if editingRow}
        {@const target = editingRow}
        <Button variant="danger" disabled={saving} onclick={() => (confirmingRemove = target)}>
          Remove
        </Button>
        <span class="mx-1 h-5 w-px shrink-0 bg-border"></span>
      {/if}
      <Button variant="ghost" onclick={closeForm} disabled={saving}>Cancel</Button>
      <Button
        variant="primary"
        disabled={saving || !isDirty || validationError !== null}
        title={!isDirty ? 'No changes to save' : validationError ?? undefined}
        onclick={save}>
        <Icon name="check" size={14} />
        {saving ? 'Saving…' : !isDirty ? 'No changes' : validationError ? 'Fix errors' : 'Save'}
      </Button>
    {/snippet}
  </Modal>
{/if}

{#if confirmingRemove}
  {@const target = confirmingRemove}
  <Modal title="Remove indexer" width="max-w-lg" onclose={() => (confirmingRemove = null)}>
    <div class="flex flex-col gap-3 p-4">
      <p class="text-base text-ink">{target.name}</p>
      <p class="text-base text-ink-secondary">
        Caravan stops searching this source. Nothing already grabbed or imported is affected —
        only the configuration goes away.
      </p>
    </div>

    {#snippet footer()}
      <Button variant="ghost" onclick={() => (confirmingRemove = null)}>Cancel</Button>
      <Button variant="danger" disabled={busyID === target.id} onclick={remove}>Remove</Button>
    {/snippet}
  </Modal>
{/if}
