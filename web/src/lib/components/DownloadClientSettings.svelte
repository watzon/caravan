<script lang="ts">
  /**
   * Settings → Download clients (SPEC §5.1, §11 `/download-clients`).
   *
   * Caravan's embedded engine handles torrents with no configuration at all;
   * everything here is a machine the user chose to hand work to instead. The
   * test button is the point of the screen, for the same reason it is on
   * indexers: a client that is configured but unreachable must say so here
   * rather than swallowing every grab (SPEC §13).
   *
   * Credentials are write-only. The server never hands a password or API key
   * back (SPEC §12), so an edit form starts with a blank field over a stored
   * credential — left blank, the save keeps what is stored.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type {
    DownloadClient,
    DownloadClientInput,
    DownloadClientType,
    DownloadClientTypeInfo,
  } from '../api/types';
  import {
    DEFAULT_DOWNLOAD_CLIENT_PRIORITY,
    FALLBACK_DOWNLOAD_CLIENT_TYPES,
    describeType,
    validateDownloadClient,
  } from '../downloadClient';
  import { useI18n } from '../i18n.svelte';
  import { pushToast } from '../state/toast.svelte';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import DownloadRouting from './DownloadRouting.svelte';
  import EmptyState from './EmptyState.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';
  import LoadError from './LoadError.svelte';
  import Modal from './Modal.svelte';
  import SettingsCard from './SettingsCard.svelte';
  import Skeleton from './Skeleton.svelte';
  import TextInput from './TextInput.svelte';
  import Toggle from './Toggle.svelte';

  const { t } = useI18n();

  /** The result of the last test per client id, so the row can say what happened. */
  type TestResult = { ok: boolean; message: string };

  let clients = $state<DownloadClient[] | null>(null);
  let types = $state<DownloadClientTypeInfo[]>(FALLBACK_DOWNLOAD_CLIENT_TYPES);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let tests = $state<Record<number, TestResult>>({});
  let testingID = $state<number | null>(null);
  let busyID = $state<number | null>(null);
  let confirmingRemove = $state<DownloadClient | null>(null);

  /** null = the form is closed; 0 = adding; otherwise the id being edited. */
  let editingID = $state<number | null>(null);
  let saving = $state(false);
  let formTesting = $state(false);
  let formError = $state<string | null>(null);
  let formTest = $state<TestResult | null>(null);

  let type = $state<DownloadClientType>('qbittorrent');
  let name = $state('');
  let url = $state('');
  let username = $state('');
  let password = $state('');
  let apiKey = $state('');
  let category = $state('');
  let priority = $state(String(DEFAULT_DOWNLOAD_CLIENT_PRIORITY));
  let maxConcurrent = $state('');
  let enabled = $state(true);

  /**
   * Whether the row being edited already has a credential stored. It is what
   * lets a blank field mean "keep it" instead of "there is none".
   */
  let storedPassword = $state(false);
  let storedAPIKey = $state(false);

  let typeInfo = $derived(describeType(types, type));

  /** Captured once per open form, so Save reflects a real draft change. */
  let initialDraft = $state('');

  function draftSnapshot(): string {
    return JSON.stringify({
      type,
      name,
      url,
      username,
      password,
      apiKey,
      category,
      priority,
      maxConcurrent,
      enabled,
    });
  }

  function nonNegativeInteger(
    value: string,
    label: string,
    allowEmpty = false,
  ): { value: number | null; error: string | null } {
    const trimmed = value.trim();
    if (allowEmpty && trimmed === '') return { value: null, error: null };
    if (!allowEmpty && trimmed === '') {
      return { value: null, error: t('component.downloadClient.wholeNumber', { label }) };
    }
    const parsed = Number(trimmed);
    if (!Number.isFinite(parsed) || !Number.isSafeInteger(parsed) || parsed < 0) {
      return {
        value: null,
        error: allowEmpty
          ? t('component.downloadClient.optionalWholeNumber', { label })
          : t('component.downloadClient.wholeNumber', { label }),
      };
    }
    return { value: parsed, error: null };
  }

  function validationIssue(): string | null {
    const clientError = validateDownloadClient({
      name,
      url,
      username,
      apiKey,
      type:
        editingID !== 0 && !typeInfo.supported
          ? { ...typeInfo, uses_login: false, uses_api_key: false }
          : typeInfo,
      hasStoredCredential: storedAPIKey,
    });
    if (clientError) return clientError;
    if (editingID === 0 && !typeInfo.supported) {
      return t('component.downloadClient.newClientUnavailable', { type: typeInfo.label });
    }
    return (
      nonNegativeInteger(priority, t('component.downloadClient.priority')).error ??
      nonNegativeInteger(maxConcurrent, t('component.downloadClient.maxConcurrent'), true).error
    );
  }

  let isDirty = $derived(editingID !== null && draftSnapshot() !== initialDraft);
  let validationError = $derived(validationIssue());

  async function load() {
    loading = true;
    try {
      // The type list is a nicety; a failure there must not hide the clients.
      const [rows, kinds] = await Promise.all([
        api.listDownloadClients(),
        api.downloadClientTypes().catch(() => [] as DownloadClientTypeInfo[]),
      ]);
      clients = rows;
      if (kinds.length > 0) types = kinds;
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
    formTest = null;
    type = 'qbittorrent';
    name = '';
    url = '';
    username = '';
    password = '';
    apiKey = '';
    category = '';
    priority = String(DEFAULT_DOWNLOAD_CLIENT_PRIORITY);
    maxConcurrent = '';
    enabled = true;
    storedPassword = false;
    storedAPIKey = false;
    initialDraft = draftSnapshot();
  }

  function openEdit(client: DownloadClient) {
    editingID = client.id;
    formError = null;
    formTest = null;
    type = client.type;
    name = client.name;
    url = client.url;
    username = client.username;
    // Never pre-filled: the server did not hand these back.
    password = '';
    apiKey = '';
    category = client.category;
    priority = String(client.priority);
    maxConcurrent = client.max_concurrent === null ? '' : String(client.max_concurrent);
    enabled = client.enabled;
    storedPassword = client.has_password;
    storedAPIKey = client.has_api_key;
    initialDraft = draftSnapshot();
  }

  function closeForm() {
    editingID = null;
    formError = null;
    formTest = null;
  }

  /**
   * The body for a save or a test. A blank credential is omitted rather than
   * sent as "", which is what makes the server keep the stored one.
   *
   * validate() runs immediately before every caller, so these values are known
   * to be safe non-negative integers when a request is made.
   */
  function formBody(): DownloadClientInput {
    const body: DownloadClientInput = {
      type,
      name: name.trim(),
      url: url.trim(),
      username: typeInfo.uses_login ? username.trim() : '',
      category: category.trim(),
      priority: nonNegativeInteger(priority, t('component.downloadClient.priority')).value!,
      max_concurrent: nonNegativeInteger(
        maxConcurrent,
        t('component.downloadClient.maxConcurrent'),
        true,
      ).value,
      enabled,
    };
    if (typeInfo.uses_login && password !== '') body.password = password;
    if (typeInfo.uses_api_key && apiKey.trim() !== '') body.api_key = apiKey.trim();
    return body;
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
        await api.addDownloadClient(body);
        pushToast(t('component.downloadClient.added', { name: body.name }), 'success');
        closeForm();
        await load();
      } else if (editingID !== null) {
        const saved = await api.updateDownloadClient(editingID, body);
        clients = (clients ?? []).map((client) => (client.id === saved.id ? saved : client));
        // Keep the editor open after an update and take a fresh snapshot of the
        // server's canonical values. Save is disabled until the user changes
        // the new draft again.
        openEdit(saved);
        pushToast(t('component.downloadClient.saved', { name: body.name }), 'success');
      }
    } catch (err) {
      formError = errorText(err);
    } finally {
      saving = false;
    }
  }

  /** Test what is on screen, saved or not. */
  async function testForm() {
    if (!validate()) return;
    const body = formBody();
    // Editing: the id tells the server which stored credential a blank field
    // falls back to.
    if (editingID !== null && editingID !== 0) body.id = editingID;

    formTesting = true;
    formTest = null;
    try {
      await api.testDownloadClientConfig(body);
      formTest = { ok: true, message: t('component.downloadClient.reachable') };
    } catch (err) {
      formTest = { ok: false, message: errorText(err) };
    } finally {
      formTesting = false;
    }
  }

  async function test(client: DownloadClient) {
    testingID = client.id;
    try {
      await api.testDownloadClient(client.id);
      tests = { ...tests, [client.id]: { ok: true, message: t('component.downloadClient.reachable') } };
    } catch (err) {
      tests = { ...tests, [client.id]: { ok: false, message: errorText(err) } };
    } finally {
      testingID = null;
    }
  }

  async function remove() {
    const client = confirmingRemove;
    if (!client) return;
    busyID = client.id;
    try {
      await api.deleteDownloadClient(client.id);
      clients = (clients ?? []).filter((c) => c.id !== client.id);
      if (editingID === client.id) closeForm();
      confirmingRemove = null;
      pushToast(t('component.downloadClient.removed', { name: client.name }), 'neutral');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busyID = null;
    }
  }

  let rows = $derived(clients ?? []);

  /** The row the open editor belongs to, which is what its Remove acts on. */
  let editingRow = $derived(rows.find((client) => client.id === editingID) ?? null);
</script>

<SettingsCard
  title={t('component.downloadClient.title')}
  description={t('component.downloadClient.description')}>
  {#snippet action()}
    <Button variant="secondary" size="sm" onclick={load}>
      <Icon name="refresh" size={14} />
      {t('component.actions.refresh')}
    </Button>
    <Button variant="primary" size="sm" onclick={openAdd}>
      <Icon name="plus" size={14} />
      {t('component.downloadClient.add')}
    </Button>
  {/snippet}

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && clients === null}
    <div class="flex flex-col gap-2">
      {#each Array.from({ length: 2 }) as _, i (i)}
        <Skeleton class="h-14 w-full rounded-md" />
      {/each}
    </div>
  {:else if rows.length === 0}
    <EmptyState
      icon="link"
      title={t('component.downloadClient.emptyTitle')}
      message={t('component.downloadClient.emptyMessage')}>
      {#snippet action()}
        <Button variant="primary" onclick={openAdd}>{t('component.downloadClient.add')}</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <ul class="flex flex-col gap-2">
      {#each rows as client (client.id)}
        {@const result = tests[client.id]}
        {@const info = describeType(types, client.type)}
        <li class="flex flex-wrap items-center gap-3 rounded-md border border-border bg-surface px-3 py-3">
          <span
            aria-hidden="true"
            class="size-2 shrink-0 rounded-full {client.enabled ? 'bg-success' : 'bg-ink-muted'}">
          </span>

          <div class="min-w-0 flex-1">
            <p class="flex flex-wrap items-center gap-2">
              <span class="truncate text-base font-medium text-ink" title={client.name}>
                {client.name}
              </span>
              <Badge mono tone={info.protocol === 'torrent' ? 'accent' : 'info'}>
                {info.label}
              </Badge>
              <Badge tone={client.enabled ? 'success' : 'neutral'}>
                {client.enabled ? t('component.downloadClient.enabled') : t('component.downloadClient.disabled')}
              </Badge>
              {#if !info.supported}
                <Badge tone="warning">{t('component.downloadClient.notSupported')}</Badge>
              {/if}
            </p>
            <p class="truncate font-mono text-xs text-ink-muted" title={client.url}>
              {client.url}
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
              disabled={testingID === client.id}
              onclick={() => test(client)}>
              {testingID === client.id ? t('component.actions.testing') : t('component.actions.test')}
            </Button>
            <Button variant="ghost" size="sm" onclick={() => openEdit(client)}>{t('component.actions.edit')}</Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={busyID === client.id}
              onclick={() => (confirmingRemove = client)}>
              <Icon name="trash" size={14} />
              <span class="sr-only">{t('component.downloadClient.removeNamed', { name: client.name })}</span>
            </Button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}

  <!--
    Routing is the card's last row on purpose: the pickers offer the clients
    above, so the order on screen is the order the user configures them in. It
    owns its own settings fetch, like the list owns the client fetch.
  -->
  {#if clients !== null}
    <DownloadRouting clients={rows} {types} />
  {/if}
</SettingsCard>

{#if editingID !== null}
  <Modal
    title={editingID === 0 ? t('component.downloadClient.addTitle') : t('component.downloadClient.editTitle')}
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
        label={t('component.downloadClient.type')}
        help={`${t('component.downloadClient.typeHelp', {
          type: typeInfo.label,
          protocol: typeInfo.protocol,
        })}${typeInfo.supported ? '' : ` ${t('component.downloadClient.typeUnsupported')}`}`}>
        <div class="flex flex-wrap gap-2" role="radiogroup" aria-label={t('component.downloadClient.type')}>
          {#each types as option (option.type)}
            {@const unavailable = !option.supported && (editingID === 0 || type !== option.type)}
            <button
              type="button"
              role="radio"
              aria-checked={type === option.type}
              disabled={unavailable}
              title={unavailable
                ? t('component.downloadClient.typeUnavailable', { type: option.label })
                : undefined}
              onclick={() => (type = option.type)}
              class="h-8 rounded-full border px-3 text-sm transition-colors duration-150 ease-out
                     {type === option.type
                ? 'border-accent bg-accent-tint text-accent-text'
                : 'border-border bg-raised text-ink-secondary hover:text-ink'}">
              {option.label}
            </button>
          {/each}
        </div>
        {#if editingID === 0 && types.some((option) => !option.supported)}
          <p class="text-sm text-ink-secondary">
            {t('component.downloadClient.unsupportedTypes')}
          </p>
        {/if}
      </Field>

      <Field label={t('component.downloadClient.name')} for="client-name" help={t('component.downloadClient.nameHelp')}>
        <TextInput id="client-name" bind:value={name} placeholder={t('component.downloadClient.namePlaceholder')} />
      </Field>

      <Field
        label={t('component.downloadClient.baseURL')}
        for="client-url"
        help={t('component.downloadClient.baseURLHelp')}>
        <TextInput id="client-url" bind:value={url} mono placeholder="http://127.0.0.1:8080" />
      </Field>

      {#if typeInfo.uses_login}
        <Field label={t('component.downloadClient.username')} for="client-username">
          <TextInput
            id="client-username"
            bind:value={username}
            mono
            placeholder={t('component.downloadClient.usernamePlaceholder')} />
        </Field>

        <Field
          label={t('component.downloadClient.password')}
          for="client-password"
          help={storedPassword
            ? t('component.downloadClient.passwordStored')
            : t('component.downloadClient.credentialStorage')}>
          <TextInput
            id="client-password"
            bind:value={password}
            type="password"
            mono
            placeholder={storedPassword
              ? t('component.downloadClient.unchanged')
              : t('component.downloadClient.passwordPlaceholder')} />
        </Field>
      {/if}

      {#if typeInfo.uses_api_key}
        <Field
          label={t('component.downloadClient.apiKey')}
          for="client-api-key"
          help={storedAPIKey
            ? t('component.downloadClient.apiKeyStored')
            : t('component.downloadClient.credentialStorage')}>
          <TextInput
            id="client-api-key"
            bind:value={apiKey}
            type="password"
            mono
            placeholder={storedAPIKey
              ? t('component.downloadClient.unchanged')
              : t('component.downloadClient.passwordPlaceholder')} />
        </Field>
      {/if}

      <Field
        label={t('component.downloadClient.category')}
        for="client-category"
        help={t('component.downloadClient.categoryHelp')}>
        <TextInput
          id="client-category"
          bind:value={category}
          mono
          placeholder={t('component.downloadClient.categoryPlaceholder')} />
      </Field>

      <div data-settings-advanced>
        <Field
          label={t('component.downloadClient.priority')}
          for="client-priority"
          help={t('component.downloadClient.priorityHelp')}>
          <TextInput id="client-priority" bind:value={priority} oninput={() => (formError = null)} mono placeholder="25" />
        </Field>
      </div>

      <div data-settings-advanced>
        <Field
          label={t('component.downloadClient.maxConcurrent')}
          for="client-max-concurrent"
          help={t('component.downloadClient.maxConcurrentHelp')}>
          <TextInput id="client-max-concurrent" bind:value={maxConcurrent} oninput={() => (formError = null)} mono placeholder="0" />
        </Field>
      </div>

      <Toggle checked={enabled} label={t('component.downloadClient.enabled')} onchange={(next) => (enabled = next)} />

      {#if formError || (isDirty && validationError)}
        <p class="text-sm text-danger">{formError ?? validationError}</p>
      {/if}
    </form>

    {#snippet footer()}
      <div class="mr-auto flex min-w-0 items-center gap-2">
        <Button variant="secondary" onclick={testForm} disabled={formTesting || saving}>
          {formTesting ? t('component.actions.testing') : t('component.actions.test')}
        </Button>
        {#if formTest}
          <p
            class="truncate text-sm {formTest.ok ? 'text-success' : 'text-danger'}"
            title={formTest.message}>
            {formTest.ok ? '✓ ' : '✕ '}{formTest.message}
          </p>
        {/if}
      </div>

      {#if editingRow}
        {@const target = editingRow}
        <Button variant="danger" disabled={saving} onclick={() => (confirmingRemove = target)}>
          {t('component.actions.remove')}
        </Button>
        <span class="mx-1 h-5 w-px shrink-0 bg-border"></span>
      {/if}
      <Button variant="ghost" onclick={closeForm} disabled={saving}>{t('component.actions.cancel')}</Button>
      <Button
        variant="primary"
        disabled={saving || !isDirty || validationError !== null}
        title={!isDirty ? t('component.downloadClient.noChangesToSave') : validationError ?? undefined}
        onclick={save}>
        <Icon name="check" size={14} />
        {saving ? t('component.actions.saving') : !isDirty ? t('component.actions.noChanges') : validationError ? t('component.actions.fixErrors') : t('component.actions.save')}
      </Button>
    {/snippet}
  </Modal>
{/if}

{#if confirmingRemove}
  {@const target = confirmingRemove}
  <Modal title={t('component.downloadClient.removeTitle')} width="max-w-lg" onclose={() => (confirmingRemove = null)}>
    <div class="flex flex-col gap-3 p-4">
      <p class="text-base text-ink">{target.name}</p>
      <p class="text-base text-ink-secondary">
        {t('component.downloadClient.removeDescription')}
      </p>
    </div>

    {#snippet footer()}
      <Button variant="ghost" onclick={() => (confirmingRemove = null)}>{t('component.actions.cancel')}</Button>
      <Button variant="danger" disabled={busyID === target.id} onclick={remove}>{t('component.actions.remove')}</Button>
    {/snippet}
  </Modal>
{/if}
