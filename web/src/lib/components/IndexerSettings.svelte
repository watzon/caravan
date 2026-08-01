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
  let categories = $state('');
  let enabled = $state(true);

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
    categories = '';
    enabled = true;
    resetCategoryPicker();
  }

  function openEdit(indexer: Indexer) {
    editingID = indexer.id;
    formError = null;
    name = indexer.name;
    type = indexer.type;
    url = indexer.url;
    apiKey = indexer.api_key;
    categories = formatCategories(indexer.categories);
    enabled = indexer.enabled;
    resetCategoryPicker();
    selectedCategories = indexer.categories;
    // Best effort: a failure leaves the free-text field as the editor.
    void loadCategories();
  }

  function closeForm() {
    editingID = null;
    formError = null;
    resetCategoryPicker();
  }

  async function save() {
    const problem = validateIndexer({ name, url });
    if (problem) {
      formError = problem;
      return;
    }

    const body: IndexerInput = {
      name: name.trim(),
      type,
      url: url.trim(),
      api_key: apiKey.trim(),
      categories: treeUsable ? selectedCategories : parseCategories(categories),
      enabled,
    };

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
</script>

<section class="flex flex-col gap-4">
  <div class="flex flex-wrap items-center gap-3">
    <p class="text-base text-ink-secondary">
      Search sources. Caravan ships with none: every indexer here is one you added.
    </p>
    <div class="ml-auto flex items-center gap-2">
      <Button variant="secondary" onclick={load}>
        <Icon name="refresh" size={14} />
        Refresh
      </Button>
      <Button variant="primary" onclick={openAdd} disabled={editingID === 0}>
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
  {:else if rows.length === 0 && editingID === null}
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

  {#if editingID !== null}
    <form
      class="flex flex-col gap-4 rounded-lg border border-border-strong bg-surface p-4"
      onsubmit={(event) => {
        event.preventDefault();
        void save();
      }}>
      <h3 class="text-lg font-semibold text-ink">
        {editingID === 0 ? 'Add indexer' : 'Edit indexer'}
      </h3>

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
        <TextInput id="indexer-key" bind:value={apiKey} type="password" mono placeholder="•••••" />
      </Field>

      <Field
        label="Categories"
        for={treeUsable ? undefined : 'indexer-categories'}
        help={treeUsable
          ? 'Pick what Caravan searches on this indexer. Nothing selected means Caravan chooses per search.'
          : 'Load the list from the indexer, or enter category ids by hand. Empty means Caravan chooses per search.'}>
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
            {#if categoriesError}
              <p class="text-sm text-danger">{categoriesError}</p>
            {/if}
          </div>
        </div>
      </Field>

      <Toggle
        checked={enabled}
        label="Enabled"
        onchange={(next) => (enabled = next)} />

      {#if formError}
        <p class="text-sm text-danger">{formError}</p>
      {/if}

      <div class="flex gap-2">
        <Button variant="primary" type="submit" disabled={saving}>
          <Icon name="check" size={14} />
          {saving ? 'Saving…' : 'Save'}
        </Button>
        <Button variant="ghost" onclick={closeForm} disabled={saving}>Cancel</Button>
      </div>
    </form>
  {/if}
</section>

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
