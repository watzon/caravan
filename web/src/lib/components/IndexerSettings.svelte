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
  import type { Indexer, IndexerCategory, IndexerDefinition, IndexerExecutionStatus, IndexerInput, IndexerInventoryEntry, IndexerKind, IndexerType } from '../api/types';
  import {
    INDEXER_KINDS,
    INDEXER_TYPES,
    catalogContentValues,
    catalogLanguages,
    catalogPrivacyValues,
    filterDefinitions,
    feedURLFromBase,
    formatCategories,
    indexerFormURL,
    parseCategories,
    toggleFilterValue,
    urlHost,
    validateIndexer,
  } from '../indexer';
  import { useI18n } from '../i18n.svelte';
  import type { AppliedChip } from '../explore';
  import { pushToast } from '../state/toast.svelte';
  import AppliedChips from './AppliedChips.svelte';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import CategoryPicker from './CategoryPicker.svelte';
  import Dropdown from './Dropdown.svelte';
  import EmptyState from './EmptyState.svelte';
  import Field from './Field.svelte';
  import FilterOptions from './FilterOptions.svelte';
  import FilterPill from './FilterPill.svelte';
  import Icon from './Icon.svelte';
  import LoadError from './LoadError.svelte';
  import Modal from './Modal.svelte';
  import Skeleton from './Skeleton.svelte';
  import TextInput from './TextInput.svelte';
  import Toggle from './Toggle.svelte';

  /** The result of the last test per indexer id, so the row can say what happened. */
  type TestResult = { ok: boolean; message: string };
  const { t } = useI18n();

  let indexers = $state<Indexer[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let tests = $state<Record<number, TestResult>>({});
  let testingID = $state<number | null>(null);
  let busyID = $state<number | null>(null);
  let confirmingRemove = $state<Indexer | null>(null);

  /** null = the form is closed; 0 = adding; otherwise the id being edited. */
  let editingID = $state<number | null>(null);
  let addStep = $state<'kind' | 'pick' | 'details'>('details');
  let selectedKind = $state<IndexerKind | null>(null);
  let selectedDef = $state<IndexerDefinition | null>(null);
  let definitionID = $state('');
  let definitionSource = $state('');
  let definitionRevision = $state('');
  let definitionDigest = $state('');
  let definitionSettings = $state<Record<string, string>>({});
  /**
   * Setting names stored on the indexer being edited. Values are write-only
   * server-side, so the edit form renders blank inputs for these names and
   * only sends the ones the user fills in (the server merges the rest).
   */
  let editSettingNames = $state<string[]>([]);
  let catalog = $state<IndexerDefinition[]>([]);
  let inventory = $state<IndexerInventoryEntry[]>([]);
  let catalogQuery = $state('');
  let catalogPrivacy = $state<string[]>([]);
  let catalogLangs = $state<string[]>([]);
  let catalogMedia = $state<string[]>([]);
  let catalogShowUnsupported = $state(false);
  let catalogLoading = $state(false);
  let catalogError = $state<string | null>(null);
  let catalogAbort: AbortController | null = null;
  let saving = $state(false);
  let formError = $state<string | null>(null);

  let name = $state('');
  let type = $state<IndexerType>('torznab');
  let url = $state('');
  let selectedBase = $state('');
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
      definitionID,
      definitionSource,
      definitionRevision,
      definitionDigest,
      definitionSettings,
      apiKey,
      clearAPIKey,
      categories,
      priority,
      selectedCategories,
      enabled,
    });
  }

  let isDirty = $derived(
    editingID !== null &&
      ((adding && addStep === 'details') || draftSnapshot() !== initialDraft),
  );
  let priorityError = $derived.by(() => {
    const value = Number(priority.trim());
    return priority.trim() === '' || !Number.isInteger(value) || value < 0
      ? t('component.indexers.priorityError')
      : null;
  });
  let validationError = $derived(
    validateIndexer({
      name,
      url,
      apiKey,
      // Definition-backed indexers take their credentials through the
      // definition settings form; the standalone API-key field is not
      // rendered for them, so it must not gate the save either.
      requiresAPIKey: Boolean(selectedDef?.requires_api_key) && !definitionID,
      hasStoredKey: hasAPIKey && !clearAPIKey,
    }) ?? priorityError,
  );
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
  let catalogHits = $derived(
    filterDefinitions(catalog, catalogQuery, {
      privacy: catalogPrivacy,
      languages: catalogLangs,
      content: catalogMedia,
    }),
  );
  let inventoryHits = $derived.by(() => {
    if (selectedKind !== 'torrent') return [];
    const operationalIDs = new Set(catalog.map((definition) => definition.id.toLowerCase()));
    const query = catalogQuery.trim().toLowerCase();
    return inventory.filter((entry) => {
      if (operationalIDs.has(entry.id.toLowerCase())) return false;
      if (!catalogShowUnsupported && !entry.definitions?.some(canChooseVariant)) return false;
      if (query && !`${entry.id} ${entry.name} ${entry.description}`.toLowerCase().includes(query)) return false;
      if (catalogPrivacy.length > 0 && !catalogPrivacy.includes(entry.privacy)) return false;
      if (catalogLangs.length > 0 && !catalogLangs.includes(entry.language)) return false;
      if (catalogMedia.length > 0 && !entry.content.some((tag) => catalogMedia.includes(tag))) return false;
      return true;
    });
  });
  let showCatalogFilters = $derived(selectedKind === 'torrent' || selectedKind === 'usenet');
  let privacyOptions = $derived(
    sortedDistinct([...catalogPrivacyValues(catalog), ...inventory.map((entry) => entry.privacy)]).map((id) => ({ id, name: privacyLabel(id) })),
  );
  let languageOptions = $derived(
    sortedDistinct([...catalogLanguages(catalog), ...inventory.map((entry) => entry.language)]).map((id) => ({ id, name: languageLabel(id) })),
  );
  let contentOptions = $derived(
    sortedDistinct([...catalogContentValues(catalog), ...inventory.flatMap((entry) => entry.content)]).map((id) => ({ id, name: contentLabel(id) })),
  );
  let catalogFilterChips = $derived.by((): AppliedChip[] => {
    const chips: AppliedChip[] = [];
    for (const id of catalogPrivacy) chips.push({ key: `privacy:${id}`, label: privacyLabel(id) });
    for (const id of catalogLangs) chips.push({ key: `language:${id}`, label: languageLabel(id) });
    for (const id of catalogMedia) chips.push({ key: `content:${id}`, label: contentLabel(id) });
    return chips;
  });
  let adding = $derived(editingID === 0);
  let onDetails = $derived(editingID !== null && (editingID > 0 || addStep === 'details'));
  let modalTitle = $derived.by(() => {
    if (editingID === null) return t('component.indexers.add');
    if (editingID > 0) return t('component.indexers.edit');
    if (addStep === 'kind') return t('component.indexers.chooseKind');
    if (addStep === 'pick') {
      const kind = INDEXER_KINDS.find((option) => option.value === selectedKind);
      return t('component.indexers.chooseIndexer', { kind: kind?.label ?? '' });
    }
    return t('component.indexers.add');
  });

  function privacyLabel(privacy: string): string {
    if (privacy === 'public') return t('indexer.privacy.public');
    if (privacy === 'semi-private') return t('indexer.privacy.semi-private');
    return t('indexer.privacy.private');
  }

  function contentLabel(tag: string): string {
    switch (tag) {
      case 'movies':
        return t('indexer.content.movies');
      case 'tv':
        return t('indexer.content.tv');
      case 'anime':
        return t('indexer.content.anime');
      case 'audio':
        return t('indexer.content.audio');
      case 'books':
        return t('indexer.content.books');
      case 'adult':
        return t('indexer.content.adult');
      case 'pc':
        return t('indexer.content.pc');
      case 'other':
        return t('indexer.content.other');
      default:
        return tag;
    }
  }

  function sortedDistinct(values: string[]): string[] {
    return [...new Set(values.filter(Boolean))].sort((left, right) => left.localeCompare(right));
  }

  function inventoryStateLabel(state: IndexerInventoryEntry['state']): string {
    switch (state) {
      case 'unsupported':
        return t('component.indexers.unsupported');
      case 'quarantined':
        return t('component.indexers.quarantined');
      case 'runnable-unverified':
        return t('component.indexers.runnableUnverified');
      case 'verified':
        return t('component.indexers.verified');
      case 'source-not-installed':
        return t('component.indexers.sourceNotInstalled');
      default:
        return t('component.indexers.metadataOnly');
    }
  }

  function inventoryBlockedCodes(entry: IndexerInventoryEntry): string[] {
    return [...new Set((entry.definitions ?? []).flatMap((definition) =>
      definition.blocked_code ? [definition.blocked_code] : [],
    ))];
  }

  function inventoryUnsupportedCount(entry: IndexerInventoryEntry): number {
    return (entry.definitions ?? []).reduce(
      (total, definition) => total + (definition.unsupported?.length ?? 0),
      0,
    );
  }

  function languageLabel(code: string): string {
    try {
      return new Intl.DisplayNames(['en'], { type: 'language' }).of(code) ?? code;
    } catch {
      return code;
    }
  }

  function clearCatalogFilters() {
    catalogPrivacy = [];
    catalogLangs = [];
    catalogMedia = [];
  }

  function removeCatalogFilter(key: string) {
    const [facet, value] = key.split(':');
    if (!value) return;
    if (facet === 'privacy') catalogPrivacy = catalogPrivacy.filter((id) => id !== value);
    if (facet === 'language') catalogLangs = catalogLangs.filter((id) => id !== value);
    if (facet === 'content') catalogMedia = catalogMedia.filter((id) => id !== value);
  }

  function privacyTone(privacy: string): 'success' | 'info' | 'warning' {
    if (privacy === 'public') return 'success';
    if (privacy === 'semi-private') return 'warning';
    return 'info';
  }

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
      const exactPin =
        Boolean(definitionID) &&
        Boolean(definitionSource) &&
        Boolean(definitionRevision) &&
        Boolean(definitionDigest);
      // An existing indexer keeps its credentials write-only, so its
      // category probe must run server-side with the stored config.
      const tree =
        editingID !== null && editingID !== 0
          ? await api.indexerStoredCategories(editingID, ac.signal)
          : await api.indexerCategories(
              {
                url: url.trim(),
                api_key: apiKey.trim(),
                type,
                definition_id: definitionID || undefined,
                definition_source: exactPin ? definitionSource : undefined,
                definition_revision: exactPin ? definitionRevision : undefined,
                definition_digest: exactPin ? definitionDigest : undefined,
                settings: definitionID ? definitionSettings : undefined,
              },
              ac.signal,
            );
      if (categoriesAbort !== ac) return;
      categoryTree = tree;
      if (tree.length === 0) {
        categoriesError = t('component.indexers.noCategories');
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

  function applyDefinition(def: IndexerDefinition) {
    selectedDef = def;
    definitionID = def.definition_id ?? '';
    definitionSource = def.definition_source ?? '';
    definitionRevision = def.definition_revision ?? '';
    definitionDigest = def.definition_digest ?? '';
    definitionSettings = Object.fromEntries(
      (def.settings ?? [])
        .filter((setting) => setting.type !== 'info' && setting.editable !== false)
        .map((setting) => [setting.name, setting.default ?? '']),
    );
    editSettingNames = [];
    name = def.name;
    type = def.protocol;
    selectedBase = (def.urls?.[0] || def.url || '').replace(/\/+$/, '');
    url = indexerFormURL(def);
    apiKey = '';
    hasAPIKey = false;
    clearAPIKey = false;
    categories = formatCategories(def.categories);
    priority = '25';
    enabled = true;
    resetCategoryPicker();
    selectedCategories = def.categories ?? [];
    formError = null;
    initialDraft = draftSnapshot();
  }

  function chooseBase(base: string) {
    selectedBase = base.replace(/\/+$/, '');
    url = definitionID ? selectedBase : feedURLFromBase(base);
  }

  async function loadCatalog(kind: IndexerKind) {
    catalogAbort?.abort();
    const ac = new AbortController();
    catalogAbort = ac;
    catalogLoading = true;
    catalogError = null;
    try {
      const rows = await api.listIndexerCatalog(kind, undefined, ac.signal);
      if (catalogAbort !== ac) return;
      catalog = rows.definitions;
      inventory = rows.inventory;
    } catch (err) {
      if (catalogAbort !== ac) return;
      catalogError = errorText(err);
      catalog = [];
      inventory = [];
    } finally {
      if (catalogAbort === ac) catalogLoading = false;
    }
  }

  function openAdd() {
    editingID = 0;
    addStep = 'kind';
    selectedKind = null;
    selectedDef = null;
    definitionID = '';
    definitionSource = '';
    definitionRevision = '';
    definitionDigest = '';
    definitionSettings = {};
    editSettingNames = [];
    catalog = [];
    inventory = [];
    catalogQuery = '';
    clearCatalogFilters();
    catalogShowUnsupported = false;
    catalogError = null;
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

  async function chooseKind(kind: IndexerKind) {
    selectedKind = kind;
    catalogQuery = '';
    clearCatalogFilters();
    catalogShowUnsupported = false;
    addStep = 'pick';
    await loadCatalog(kind);
  }

  function chooseDefinition(def: IndexerDefinition) {
    applyDefinition({
      ...def,
      definition_source: undefined,
      definition_revision: undefined,
      definition_digest: undefined,
    });
    addStep = 'details';
  }

  function canChooseVariant(variant: IndexerExecutionStatus): boolean {
    // Managed and builtin definitions carry no exact pin; only immutable
    // pack sources require revision and digest before they are addable.
    const unpinned = variant.source?.trim() === 'managed' || variant.source?.trim() === 'builtin';
    return (
      variant.addable === true &&
      variant.state === 'verified' &&
      Boolean(variant.definition_id?.trim()) &&
      Boolean(variant.source?.trim()) &&
      (unpinned || (Boolean(variant.revision?.trim()) && Boolean(variant.digest?.trim()))) &&
      (variant.base_urls?.length ?? 0) > 0
    );
  }

  function chooseInventoryVariant(entry: IndexerInventoryEntry, variant: IndexerExecutionStatus) {
    if (!canChooseVariant(variant)) return;
    const homepage = (variant.base_urls?.[0] ?? '').replace(/\/+$/, '');
	const managed = variant.source?.trim() === 'managed';
    applyDefinition({
      id: entry.id,
      definition_id: variant.definition_id,
	  definition_source: managed ? undefined : variant.source,
	  definition_revision: managed ? undefined : variant.revision,
	  definition_digest: managed ? undefined : variant.digest,
      name: entry.name,
      kind: 'torrent',
      protocol: 'torznab',
      privacy: entry.privacy,
      language: entry.language,
      description: entry.description,
      info_url: entry.info_url,
      url: homepage,
      urls: variant.base_urls ?? [],
      url_placeholder: '',
      requires_api_key: entry.requires_api_key,
      categories: [],
      content: entry.content,
      settings: variant.settings,
    });
    addStep = 'details';
  }

  function backFromPick() {
    addStep = 'kind';
    selectedKind = null;
    catalog = [];
    inventory = [];
    catalogQuery = '';
    clearCatalogFilters();
    catalogError = null;
    catalogAbort?.abort();
  }

  function backFromDetails() {
    if (!adding) return;
    addStep = 'pick';
    formError = null;
    resetCategoryPicker();
  }

  function openEdit(indexer: Indexer) {
    editingID = indexer.id;
    addStep = 'details';
    selectedKind = null;
    selectedDef = null;
    definitionID = indexer.definition_id ?? '';
    definitionSource = indexer.definition_source ?? '';
    definitionRevision = indexer.definition_revision ?? '';
    definitionDigest = indexer.definition_digest ?? '';
    editSettingNames = indexer.has_settings ?? [];
    definitionSettings = Object.fromEntries(editSettingNames.map((settingName) => [settingName, '']));
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
    addStep = 'details';
    selectedKind = null;
    selectedDef = null;
    definitionID = '';
    definitionSource = '';
    definitionRevision = '';
    definitionDigest = '';
    definitionSettings = {};
    editSettingNames = [];
    catalog = [];
    catalogQuery = '';
    clearCatalogFilters();
    catalogError = null;
    catalogAbort?.abort();
    formError = null;
    resetCategoryPicker();
  }

  async function save() {
    if (saving || !isDirty) return;
    if (validationError) {
      formError = validationError;
      return;
    }

    const exact =
      Boolean(definitionID) &&
      Boolean(definitionSource) &&
      Boolean(definitionRevision) &&
      Boolean(definitionDigest);
    const body: IndexerInput = {
      name: name.trim(),
      type,
      url: url.trim(),
      categories: treeUsable ? selectedCategories : parseCategories(categories),
      priority: Number(priority.trim()),
      enabled,
    };
    // The add flow sends the full settings form. The edit flow sends only
    // the values the user typed: stored values are write-only, and the
    // server keeps every setting the request does not mention.
    const typedSettings = Object.fromEntries(
      Object.entries(definitionSettings).filter(([, value]) => (value ?? '').trim() !== ''),
    );
    const editedSettings = Object.keys(typedSettings).length > 0 ? typedSettings : undefined;
    if (exact) {
      body.definition_id = definitionID;
      body.definition_source = definitionSource;
      body.definition_revision = definitionRevision;
      body.definition_digest = definitionDigest;
      body.settings = selectedDef ? definitionSettings : editedSettings;
    } else if (definitionID) {
      body.definition_id = definitionID;
      body.settings = selectedDef ? definitionSettings : editedSettings;
    }
    if (apiKey.trim() !== '' || clearAPIKey) {
      body.api_key = apiKey.trim();
    }

    saving = true;
    try {
      if (editingID === 0) {
        await api.addIndexer(body);
        pushToast(t('component.indexers.added', { name: body.name }), 'success');
      } else if (editingID !== null) {
        await api.updateIndexer(editingID, body);
        pushToast(t('component.indexers.saved', { name: body.name }), 'success');
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
      tests = { ...tests, [indexer.id]: { ok: true, message: t('component.indexers.reachable') } };
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
      pushToast(t('component.indexers.removed', { name: indexer.name }), 'neutral');
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
        {t('component.indexers.refresh')}
      </Button>
      <Button variant="primary" onclick={openAdd}>
        <Icon name="plus" size={14} />
        {t('component.indexers.add')}
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
      title={t('component.indexers.emptyTitle')}
      message={t('component.indexers.emptyMessage')}>
      {#snippet action()}
        <Button variant="primary" onclick={openAdd}>{t('component.indexers.add')}</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <ul class="flex flex-col gap-2">
      {#each rows as indexer (indexer.id)}
        {@const result = tests[indexer.id]}
        <li class="flex flex-wrap items-center gap-3 rounded-md border border-border bg-surface px-3 py-3">
          <span
            aria-hidden="true"
            class="size-2 shrink-0 rounded-full {indexer.enabled ? 'bg-success' : 'bg-ink-muted'}">
          </span>

          <div class="min-w-0 flex-1">
            <p class="flex flex-wrap items-center gap-2">
              <span class="truncate text-base font-medium text-ink" title={indexer.name}>{indexer.name}</span>
              <Badge mono tone={indexer.type === 'torznab' ? 'accent' : 'info'}>
                {INDEXER_TYPES.find((option) => option.value === indexer.type)?.label ?? indexer.type}
              </Badge>
              <Badge mono tone="neutral">{t('component.indexers.priorityValue', { priority: indexer.priority ?? 25 })}</Badge>
              <Badge tone={indexer.enabled ? 'success' : 'neutral'}>
                {t(indexer.enabled ? 'component.indexers.enabled' : 'component.indexers.disabled')}
              </Badge>
              {#if indexer.health_error}
                <Badge tone="warning">{t('component.indexers.unhealthy')}</Badge>
              {/if}
            </p>
            <p class="truncate font-mono text-xs text-ink-muted" title={indexer.url}>
              {indexer.url}
            </p>
            {#if indexer.health_error && !result}
              <p class="mt-1 text-sm text-warning" title={indexer.health_error}>
                {indexer.health_error}
              </p>
            {/if}
            {#if result}
              <p class="mt-1 text-sm {result.ok ? 'text-success' : 'text-danger'}">
                {result.ok ? '✓ ' : '✕ '}{result.message}
              </p>
            {/if}
          </div>

          <div class="flex w-full shrink-0 items-center justify-end gap-2 sm:w-auto">
            <Button
              variant="secondary"
              size="sm"
              disabled={testingID === indexer.id}
              onclick={() => test(indexer)}>
              {testingID === indexer.id ? t('component.indexers.testing') : t('component.indexers.test')}
            </Button>
            <Button variant="ghost" size="sm" onclick={() => openEdit(indexer)}>{t('component.indexers.edit')}</Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={busyID === indexer.id}
              title={t('component.indexers.removeName', { name: indexer.name })}
              onclick={() => (confirmingRemove = indexer)}>
              <Icon name="trash" size={14} />
              <span class="sr-only">{t('component.indexers.removeName', { name: indexer.name })}</span>
            </Button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</section>

{#if editingID !== null}
  <Modal
    title={modalTitle}
    width={onDetails ? 'max-w-xl' : 'max-w-2xl'}
    dirty={isDirty}
    onclose={closeForm}>
    {#if adding && addStep === 'kind'}
      <div class="flex flex-col gap-2 p-4">
        {#each INDEXER_KINDS as option (option.value)}
          <button
            type="button"
            class="flex flex-col items-start gap-1 rounded-md border border-border bg-raised px-4 py-3 text-left transition-colors duration-150 ease-out hover:border-border-strong hover:bg-overlay"
            onclick={() => void chooseKind(option.value)}>
            <span class="text-base font-medium text-ink">{option.label}</span>
            <span class="text-sm text-ink-secondary">{option.help}</span>
          </button>
        {/each}
      </div>
    {:else if adding && addStep === 'pick'}
      <div class="flex flex-col gap-3 p-4">
        <Field label={t('component.indexers.searchCatalog')} for="indexer-catalog-search">
          <TextInput
            id="indexer-catalog-search"
            type="search"
            bind:value={catalogQuery}
            placeholder={t('component.indexers.searchCatalogPlaceholder')} />
        </Field>
        {#if showCatalogFilters}
          <div class="flex flex-wrap items-center gap-2">
            {#if privacyOptions.length > 0}
              <FilterPill
                label={t('component.indexers.filterPrivacy')}
                applied={catalogPrivacy.length > 0}>
                <FilterOptions
                  options={privacyOptions}
                  selected={catalogPrivacy}
                  onselect={(id) => (catalogPrivacy = toggleFilterValue(catalogPrivacy, id))} />
              </FilterPill>
            {/if}
            {#if languageOptions.length > 0}
              <FilterPill
                label={t('component.indexers.filterLanguage')}
                applied={catalogLangs.length > 0}>
                <FilterOptions
                  options={languageOptions}
                  selected={catalogLangs}
                  onselect={(id) => (catalogLangs = toggleFilterValue(catalogLangs, id))} />
              </FilterPill>
            {/if}
            {#if contentOptions.length > 0}
              <FilterPill
                label={t('component.indexers.filterContent')}
                applied={catalogMedia.length > 0}>
                <FilterOptions
                  options={contentOptions}
                  selected={catalogMedia}
                  onselect={(id) => (catalogMedia = toggleFilterValue(catalogMedia, id))} />
              </FilterPill>
            {/if}
            <Toggle
              checked={catalogShowUnsupported}
              label={t('component.indexers.showUnsupported')}
              onchange={(next) => (catalogShowUnsupported = next)} />
          </div>
          <AppliedChips
            chips={catalogFilterChips}
            onremove={removeCatalogFilter}
            onclear={clearCatalogFilters} />
        {/if}
        {#if catalogError}
          <LoadError message={catalogError} onretry={() => selectedKind && loadCatalog(selectedKind)} />
        {:else if catalogLoading && catalog.length === 0 && inventory.length === 0}
          <div class="flex flex-col gap-2">
            {#each Array.from({ length: 6 }) as _, i (i)}
              <Skeleton class="h-14 w-full rounded-md" />
            {/each}
          </div>
        {:else if catalogHits.length === 0 && inventoryHits.length === 0}
          <p class="text-sm text-ink-secondary">{t('component.indexers.noCatalogMatches')}</p>
        {:else}
          <p class="text-xs text-ink-muted">{t('component.indexers.catalogCount', { count: catalogHits.length + inventoryHits.length })}</p>
          <ul class="flex max-h-[28rem] flex-col gap-1 overflow-y-auto">
            {#each catalogHits as def (def.id)}
              <li>
                <button
                  type="button"
                  class="flex w-full flex-col items-start gap-1 rounded-md border border-transparent px-3 py-2 text-left transition-colors duration-150 ease-out hover:border-border hover:bg-raised"
                  onclick={() => chooseDefinition(def)}>
                  <span class="flex flex-wrap items-center gap-2">
                    <span class="text-base font-medium text-ink">{def.name}</span>
                    <Badge tone={privacyTone(def.privacy)}>{privacyLabel(def.privacy)}</Badge>
                    {#if def.language}
                      <Badge mono tone="neutral">{def.language}</Badge>
                    {/if}
                  </span>
                  {#if def.description}
                    <span class="line-clamp-2 text-sm text-ink-secondary">{def.description}</span>
                  {/if}
                </button>
              </li>
            {/each}
            {#each inventoryHits as entry (`inventory:${entry.id}`)}
              {@const managedDefinition = entry.definitions?.find(canChooseVariant)}
              {@const blockedCodes = inventoryBlockedCodes(entry)}
              {@const unsupportedCount = inventoryUnsupportedCount(entry)}
              <li>
                {#if managedDefinition}
                  <button
                    type="button"
                    class="flex w-full flex-col items-start gap-1 rounded-md border border-transparent px-3 py-2 text-left transition-colors duration-150 ease-out hover:border-border hover:bg-raised"
                    onclick={() => chooseInventoryVariant(entry, managedDefinition)}>
                    <span class="flex flex-wrap items-center gap-2">
                      <span class="text-base font-medium text-ink">{entry.name}</span>
                      <Badge tone={privacyTone(entry.privacy)}>{privacyLabel(entry.privacy)}</Badge>
                      {#if entry.language}
                        <Badge mono tone="neutral">{entry.language}</Badge>
                      {/if}
                    </span>
                    {#if entry.description}
                      <span class="line-clamp-2 text-sm text-ink-secondary">{entry.description}</span>
                    {/if}
                  </button>
                {:else}
                  <div class="flex flex-col items-start gap-1 rounded-md border border-transparent px-3 py-2">
                    <span class="flex flex-wrap items-center gap-2">
                      <span class="text-base font-medium text-ink">{entry.name}</span>
                      <Badge tone={privacyTone(entry.privacy)}>{privacyLabel(entry.privacy)}</Badge>
                      <Badge tone="neutral">{inventoryStateLabel(entry.state)}</Badge>
                      {#if entry.language}
                        <Badge mono tone="neutral">{entry.language}</Badge>
                      {/if}
                    </span>
                    {#if entry.description}
                      <span class="line-clamp-2 text-sm text-ink-secondary">{entry.description}</span>
                    {/if}
                    {#if blockedCodes.length > 0 || unsupportedCount > 0}
                      <span class="flex flex-wrap gap-2 pt-1">
                        {#each blockedCodes as code (code)}
                          <Badge tone="warning">{t('component.indexers.blocked', { code })}</Badge>
                        {/each}
                        {#if unsupportedCount > 0}
                          <Badge tone="warning">
                            {t('component.indexers.unsupportedCount', { count: unsupportedCount })}
                          </Badge>
                        {/if}
                      </span>
                    {/if}
                  </div>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
      </div>
    {:else}
    <form
      class="flex flex-col gap-4 p-4"
      onsubmit={(event) => {
        event.preventDefault();
        void save();
      }}>
      <Field label={t('component.indexers.name')} for="indexer-name" help={t('component.indexers.nameHelp')}>
        <TextInput id="indexer-name" bind:value={name} placeholder={t('component.indexers.namePlaceholder')} />
      </Field>

      {#if selectedDef}
        <p class="flex flex-wrap items-center gap-2 text-sm text-ink-secondary">
          <Badge mono tone={selectedDef.protocol === 'torznab' ? 'accent' : 'info'}>
            {INDEXER_TYPES.find((option) => option.value === selectedDef?.protocol)?.label ?? selectedDef.protocol}
          </Badge>
          <Badge tone={privacyTone(selectedDef.privacy)}>{privacyLabel(selectedDef.privacy)}</Badge>
          {#if selectedDef.definition_id}
            <Badge tone="success">{t('component.indexers.localAdapter')}</Badge>
          {/if}
        </p>
      {/if}

      {#if !selectedDef && !definitionID}
      <Field label={t('component.indexers.type')} help={INDEXER_TYPES.find((option) => option.value === type)?.help ?? ''}>
        <div class="flex gap-2" role="radiogroup" aria-label={t('component.indexers.type')}>
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
      {/if}

      <Field
        label={t('component.indexers.baseUrl')}
        for="indexer-url"
        help={t(selectedDef?.definition_id
          ? 'component.indexers.localBaseUrlHelp'
          : selectedDef && (selectedDef.urls?.length ?? 0) > 1
            ? 'component.indexers.mirrorHelp'
            : selectedDef && selectedDef.kind === 'torrent'
            ? 'component.indexers.torznabFeedHelp'
            : 'component.indexers.baseUrlHelp')}>
        <div class="flex flex-col gap-2">
          {#if selectedDef && (selectedBase || url)}
            <p class="text-sm text-ink-secondary">
              {t('component.indexers.baseUrl')}: {urlHost(selectedBase || url)}
            </p>
          {/if}
          {#if selectedDef && (selectedDef.urls?.length ?? 0) > 1}
            <Dropdown
              label={t('component.indexers.baseUrl')}
              shape="box"
              width="w-80"
              value={selectedDef.urls.find((entry) => entry.replace(/\/+$/, '') === selectedBase) ?? selectedDef.urls[0]}
              options={selectedDef.urls.map((entry) => ({ id: entry, name: urlHost(entry) }))}
              onselect={chooseBase} />
          {/if}
          <TextInput
            id="indexer-url"
            bind:value={url}
            mono
            placeholder={selectedDef?.url_placeholder || 'https://indexer.example/api'} />
        </div>
      </Field>

      {#if selectedDef?.definition_id && (selectedDef.settings?.length ?? 0) > 0}
        {#each selectedDef.settings ?? [] as setting (setting.name)}
          {#if setting.type === 'info' || setting.editable === false}
            <div class="rounded-md border border-border bg-raised px-3 py-2">
              <p class="text-sm font-medium text-ink">{setting.label || setting.name}</p>
              {#if setting.default}
                <p class="mt-1 text-sm text-ink-secondary">{setting.default}</p>
              {/if}
            </div>
          {:else if setting.type === 'checkbox'}
            <Toggle
              checked={definitionSettings[setting.name] === 'true'}
              label={setting.label || setting.name}
              onchange={(next) => (definitionSettings[setting.name] = String(next))} />
          {:else if setting.type === 'select'}
            <Field label={setting.label || setting.name}>
              <Dropdown
                label={setting.label || setting.name}
                shape="box"
                width="w-full"
                value={definitionSettings[setting.name] ?? ''}
                options={(setting.options ?? []).map((option) => ({ id: option.value, name: option.label }))}
                onselect={(next) => (definitionSettings[setting.name] = next)} />
            </Field>
          {:else}
            <Field label={setting.label || setting.name} for={`indexer-setting-${setting.name}`}>
            <TextInput
              id={`indexer-setting-${setting.name}`}
              bind:value={definitionSettings[setting.name]}
              type={setting.type === 'password' || setting.secret ? 'password' : 'text'}
              mono />
            </Field>
          {/if}
        {/each}
      {/if}

      {#if !selectedDef && definitionID && editSettingNames.length > 0}
        {#each editSettingNames as settingName (settingName)}
          <Field
            label={settingName}
            for={`indexer-setting-${settingName}`}
            help={t('component.indexers.settingStoredHelp')}>
            <TextInput
              id={`indexer-setting-${settingName}`}
              bind:value={definitionSettings[settingName]}
              type="password"
              mono
              placeholder="•••••" />
          </Field>
        {/each}
      {/if}

      {#if !definitionID}
      <Field
        label={t('component.indexers.apiKey')}
        for="indexer-key"
        help={t(selectedDef?.requires_api_key
          ? 'component.indexers.apiKeyRequiredHelp'
          : selectedDef
            ? 'component.indexers.apiKeyOptionalHelp'
            : 'component.indexers.apiKeyHelp')}>
        <div class="flex flex-col gap-2">
          <TextInput
            id="indexer-key"
            bind:value={apiKey}
            type="password"
            mono
            placeholder="•••••"
            oninput={() => (clearAPIKey = false)} />
          {#if hasAPIKey}
            <p class="text-sm text-ink-secondary">{t('component.indexers.keyStored')}</p>
            <Button variant="secondary" size="sm" onclick={() => (clearAPIKey = true)}>
              {t('component.indexers.clearKey')}
            </Button>
          {/if}
        </div>
      </Field>
      {/if}

      <Field
        label={t('component.indexers.priority')}
        for="indexer-priority"
        error={priorityError ?? undefined}
        help={t('component.indexers.priorityHelp')}>
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
          label={t('component.indexers.categories')}
          for={treeUsable ? undefined : 'indexer-categories'}
          help={t(treeUsable
            ? 'component.indexers.categoriesSelectedHelp'
            : 'component.indexers.categoriesManualHelp')}>
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
                  ? t('component.indexers.loading')
                  : treeUsable
                    ? t('component.indexers.reloadCategories')
                    : t('component.indexers.loadCategories')}
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
        label={t('component.indexers.enabled')}
        onchange={(next) => (enabled = next)} />

      {#if formError || (isDirty && validationError)}
        <p class="text-sm text-danger">{formError ?? validationError}</p>
      {/if}
    </form>
    {/if}

    {#snippet footer()}
      {#if editingRow}
        {@const target = editingRow}
        <Button variant="danger" disabled={saving} onclick={() => (confirmingRemove = target)}>
          {t('component.indexers.remove')}
        </Button>
        <span class="mx-1 h-5 w-px shrink-0 bg-border"></span>
      {/if}
      {#if adding && addStep === 'pick'}
        <Button variant="ghost" onclick={backFromPick}>{t('component.indexers.back')}</Button>
      {:else if adding && addStep === 'details'}
        <Button variant="ghost" onclick={backFromDetails}>{t('component.indexers.back')}</Button>
      {/if}
      <Button variant="ghost" onclick={closeForm} disabled={saving}>{t('component.indexers.cancel')}</Button>
      {#if onDetails}
      <Button
        variant="primary"
        disabled={saving || !isDirty || validationError !== null}
        title={!isDirty ? t('component.indexers.noChangesToSave') : validationError ?? undefined}
        onclick={save}>
        <Icon name="check" size={14} />
        {saving
          ? t('component.indexers.saving')
          : !isDirty
            ? t('component.indexers.noChanges')
            : validationError
              ? t('component.indexers.fixErrors')
              : t('component.indexers.save')}
      </Button>
      {/if}
    {/snippet}
  </Modal>
{/if}

{#if confirmingRemove}
  {@const target = confirmingRemove}
  <Modal title={t('component.indexers.removeTitle')} width="max-w-lg" onclose={() => (confirmingRemove = null)}>
    <div class="flex flex-col gap-3 p-4">
      <p class="text-base text-ink">{target.name}</p>
      <p class="text-base text-ink-secondary">
        {t('component.indexers.removeMessage')}
      </p>
    </div>

    {#snippet footer()}
      <Button variant="ghost" onclick={() => (confirmingRemove = null)}>{t('component.indexers.cancel')}</Button>
      <Button variant="danger" disabled={busyID === target.id} onclick={remove}>{t('component.indexers.remove')}</Button>
    {/snippet}
  </Modal>
{/if}
