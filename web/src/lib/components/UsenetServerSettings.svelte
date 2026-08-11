<script lang="ts">
  import { useI18n } from '../i18n.svelte';
  /**
   * Settings → Usenet servers (SPEC §5.1, §11 `/usenet-servers`).
   *
   * These are the article sources Caravan's built-in engine downloads from: it
   * connects to them and reads article bodies itself. That makes this screen
   * different from Download clients, where every entry is an external program
   * the user chose to hand work to — with none of those configured the engine
   * still works, but with none of these it has nowhere to read from.
   *
   * The test button is the point of the screen, as it is on indexers: a server
   * whose password is wrong must say so here rather than failing every grab
   * later (SPEC §13).
   *
   * The password is write-only. The server never hands it back (SPEC §12), so
   * an edit form starts with a blank field over a stored password — left
   * blank, the save keeps what is stored.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { UsenetServer, UsenetServerInput } from '../api/types';
  import {
    DEFAULT_USENET_MAX_CONNECTIONS,
    DEFAULT_USENET_PRIORITY,
    defaultUsenetPort,
    isDefaultUsenetPort,
    parseUsenetNumber,
    validateUsenetServer,
  } from '../usenetServer';
  import { pushToast } from '../state/toast.svelte';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import EmptyState from './EmptyState.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';
  import LoadError from './LoadError.svelte';
  import Modal from './Modal.svelte';
  import SettingsCard from './SettingsCard.svelte';
  import Skeleton from './Skeleton.svelte';
  import TextInput from './TextInput.svelte';
  import Toggle from './Toggle.svelte';

  /** The fields that make an editor draft meaningfully different from its source. */
  type UsenetDraft = {
    name: string;
    host: string;
    port: string;
    tls: boolean;
    username: string;
    password: string;
    maxConnections: string;
    priority: string;
    enabled: boolean;
  };

  /** The result of the last test per server id, so the row can say what happened. */
  type TestResult = { ok: boolean; message: string };

  let servers = $state<UsenetServer[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let tests = $state<Record<number, TestResult>>({});
  let testingID = $state<number | null>(null);
  let busyID = $state<number | null>(null);
  let confirmingRemove = $state<UsenetServer | null>(null);

  /** null = the form is closed; 0 = adding; otherwise the id being edited. */
  let editingID = $state<number | null>(null);
  let saving = $state(false);
  let formTesting = $state(false);
  let formError = $state<string | null>(null);
  let formTest = $state<TestResult | null>(null);

  let name = $state('');
  let host = $state('');
  let port = $state(String(defaultUsenetPort(true)));
  let tls = $state(true);
  let username = $state('');
  let password = $state('');
  let maxConnections = $state(String(DEFAULT_USENET_MAX_CONNECTIONS));
  let priority = $state(String(DEFAULT_USENET_PRIORITY));
  let enabled = $state(true);

  /**
   * Whether the row being edited already has a password stored. It is what lets
   * a blank field mean "keep it" instead of "there is none".
   */
  let storedPassword = $state(false);

  /** A stable copy of the form as it was when this editor was opened. */
  let initialDraft = $state<UsenetDraft | null>(null);

  function draft(): UsenetDraft {
    return { name, host, port, tls, username, password, maxConnections, priority, enabled };
  }

  function sameDraft(left: UsenetDraft, right: UsenetDraft): boolean {
    return left.name === right.name
      && left.host === right.host
      && left.port === right.port
      && left.tls === right.tls
      && left.username === right.username
      && left.password === right.password
      && left.maxConnections === right.maxConnections
      && left.priority === right.priority
      && left.enabled === right.enabled;
  }

  let dirty = $derived(initialDraft !== null && !sameDraft(draft(), initialDraft));

  function validationProblem(): string | null {
    return validateUsenetServer({
      name,
      host,
      port,
      username,
      password,
      maxConnections,
      hasStoredPassword: storedPassword && username.trim() !== '',
    });
  }

  let formValid = $derived(validationProblem() === null);

  async function load() {
    loading = true;
    try {
      servers = await api.listUsenetServers();
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
    name = '';
    host = '';
    tls = true;
    port = String(defaultUsenetPort(true));
    username = '';
    password = '';
    maxConnections = String(DEFAULT_USENET_MAX_CONNECTIONS);
    priority = String(DEFAULT_USENET_PRIORITY);
    enabled = true;
    storedPassword = false;
    initialDraft = draft();
  }

  function openEdit(server: UsenetServer) {
    editingID = server.id;
    formError = null;
    formTest = null;
    name = server.name;
    host = server.host;
    tls = server.tls;
    port = String(server.port);
    username = server.username;
    // Never pre-filled: the server did not hand this back.
    password = '';
    maxConnections = String(server.max_connections);
    priority = String(server.priority);
    enabled = server.enabled;
    storedPassword = server.has_password;
    initialDraft = draft();
  }

  function closeForm() {
    editingID = null;
    formError = null;
    formTest = null;
    initialDraft = null;
  }

  /**
   * Flipping TLS moves the port to the other protocol default, but only when
   * the box still holds a default. A port the user typed is theirs to keep.
   */
  function setTLS(next: boolean) {
    if (isDefaultUsenetPort(port)) port = String(defaultUsenetPort(next));
    tls = next;
  }

  /**
   * The body for a save or a test. A blank password is omitted rather than sent
   * as "", which is what makes the server keep the stored one.
   */
  function formBody(): UsenetServerInput {
    const body: UsenetServerInput = {
      name: name.trim(),
      host: host.trim(),
      port: parseUsenetNumber(port, defaultUsenetPort(tls)),
      tls,
      username: username.trim(),
      max_connections: parseUsenetNumber(maxConnections, DEFAULT_USENET_MAX_CONNECTIONS),
      priority: parseUsenetNumber(priority, DEFAULT_USENET_PRIORITY),
      enabled,
    };
    if (password !== '') body.password = password;
    // Clearing the username is how a server becomes anonymous, and a stored
    // password cannot outlive it: the transport refuses that pair outright.
    if (body.username === '' && storedPassword) body.password = '';
    return body;
  }

  function validate(): boolean {
    const problem = validationProblem();
    formError = problem;
    return problem === null;
  }

  async function save() {
    if (saving || !dirty || !validate()) return;
    const body = formBody();

    saving = true;
    try {
      if (editingID === 0) {
        await api.addUsenetServer(body);
        pushToast(t('component.usenetServerSettings.added', { name: body.name }), 'success');
      } else if (editingID !== null) {
        await api.updateUsenetServer(editingID, body);
        pushToast(t('component.usenetServerSettings.saved', { name: body.name }), 'success');
      }
      closeForm();
      await load();
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
    // Editing: the id tells the server which stored password a blank field
    // falls back to. It only honours that while host, port and TLS still match
    // the stored row, so retargeting the form drops the fallback.
    if (editingID !== null && editingID !== 0) body.id = editingID;

    formTesting = true;
    formTest = null;
    try {
      await api.testUsenetServerConfig(body);
      formTest = { ok: true, message: t('component.usenetServerSettings.connected') };
    } catch (err) {
      formTest = { ok: false, message: errorText(err) };
    } finally {
      formTesting = false;
    }
  }

  async function test(server: UsenetServer) {
    testingID = server.id;
    try {
      await api.testUsenetServer(server.id);
      tests = { ...tests, [server.id]: { ok: true, message: t('component.usenetServerSettings.connected') } };
    } catch (err) {
      tests = { ...tests, [server.id]: { ok: false, message: errorText(err) } };
    } finally {
      testingID = null;
    }
  }

  async function remove() {
    const server = confirmingRemove;
    if (!server) return;
    busyID = server.id;
    try {
      await api.deleteUsenetServer(server.id);
      servers = (servers ?? []).filter((s) => s.id !== server.id);
      if (editingID === server.id) closeForm();
      confirmingRemove = null;
      pushToast(t('component.usenetServerSettings.removed', { name: server.name }), 'neutral');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busyID = null;
    }
  }

  let rows = $derived(servers ?? []);

  /** The row the open editor belongs to, which is what its Remove acts on. */
  let editingRow = $derived(rows.find((server) => server.id === editingID) ?? null);

  const { t, tp } = useI18n();
</script>

<SettingsCard
  title={t('component.usenetServerSettings.usenetServers')}
  description={t('component.usenetServerSettings.newsServersTheBuiltInEngineReadsArticlesFromHigherPriorityNumbersBackUpTheFirst')}>
  {#snippet action()}
    <Button variant="secondary" size="sm" onclick={load}>
      <Icon name="refresh" size={14} />
      {t('component.usenetServerSettings.refresh')}
    </Button>
    <Button variant="primary" size="sm" onclick={openAdd}>
      <Icon name="plus" size={14} />
      {t('component.usenetServerSettings.addServer')}
    </Button>
  {/snippet}

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && servers === null}
    <div class="flex flex-col gap-2">
      {#each Array.from({ length: 2 }) as _, i (i)}
        <Skeleton class="h-14 w-full rounded-md" />
      {/each}
    </div>
  {:else if rows.length === 0}
    <EmptyState
      icon="link"
      title={t('component.usenetServerSettings.noNewsServersYet')}
      message={t('component.usenetServerSettings.caravanSBuiltInEngineReadsUsenetArticlesStraightFromYourProviderAddTheServerDetailsFromYourProviderSAccountPageToDownloadUsenetReleases')}>
      {#snippet action()}
        <Button variant="primary" onclick={openAdd}>{t('component.usenetServerSettings.addServer')}</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <ul class="flex flex-col gap-2">
      {#each rows as server (server.id)}
        {@const result = tests[server.id]}
        <li class="flex flex-wrap items-center gap-3 rounded-md border border-border bg-surface px-3 py-3">
          <span
            aria-hidden="true"
            class="size-2 shrink-0 rounded-full {server.enabled ? 'bg-success' : 'bg-ink-muted'}">
          </span>

          <div class="min-w-0 flex-1">
            <p class="flex flex-wrap items-center gap-2">
              <span class="truncate text-base font-medium text-ink" title={server.name}>
                {server.name}
              </span>
              <Badge mono tone={server.tls ? 'accent' : 'warning'}>
                {server.tls ? 'TLS' : 'Plaintext'}
              </Badge>
              <Badge mono tone="neutral">
                {t('component.usenetServerSettings.connectionCount', { count: server.max_connections })}
              </Badge>
              <Badge mono tone="neutral">{t('component.usenetServerSettings.priorityValue', { count: server.priority })}</Badge>
              <Badge tone={server.enabled ? 'success' : 'neutral'}>
                {server.enabled ? t('component.usenetServerSettings.enabled') : t('component.usenetServerSettings.disabled')}
              </Badge>
            </p>
            <p
              class="truncate font-mono text-xs text-ink-muted"
              title={`${server.host}:${server.port}${server.username ? ` · ${server.username}` : ''}`}>
              {server.host}:{server.port}
              {#if server.username}· {server.username}{/if}
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
              disabled={testingID === server.id}
              onclick={() => test(server)}>
              {testingID === server.id ? t('component.usenetServerSettings.testing') : t('component.usenetServerSettings.test')}
            </Button>
            <Button variant="ghost" size="sm" onclick={() => openEdit(server)}>{t('component.usenetServerSettings.edit')}</Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={busyID === server.id}
              onclick={() => (confirmingRemove = server)}>
              <Icon name="trash" size={14} />
              <span class="sr-only">{t('component.usenetServerSettings.removeNamed', { name: server.name })}</span>
            </Button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</SettingsCard>

{#if editingID !== null}
  <Modal
    title={editingID === 0 ? t('component.usenetServerSettings.addModal') : t('component.usenetServerSettings.editModal')}
    width="max-w-xl"
    dirty={dirty}
    onclose={closeForm}>
    <form
      class="flex flex-col gap-4 p-4"
      onsubmit={(event) => {
        event.preventDefault();
        void save();
      }}>
      <Field label={t('component.usenetServerSettings.name')} for="usenet-name" help={t('component.usenetServerSettings.howThisServerIsLabelledInErrorsAndLogs')}>
        <TextInput id="usenet-name" bind:value={name} placeholder={t('component.usenetServerSettings.eweka')} />
      </Field>

      <Field
        label={t('component.usenetServerSettings.hostname')}
        for="usenet-host"
        help={t('component.usenetServerSettings.justTheHostnameFromYourProviderNoHttpAndNoPath')}>
        <TextInput id="usenet-host" bind:value={host} mono placeholder={t('component.usenetServerSettings.newsEwekaNl')} />
      </Field>

      <Field
        label={t('component.usenetServerSettings.port')}
        for="usenet-port"
        help={t('component.usenetServerSettings.portHelp', { tlsPort: defaultUsenetPort(true), plainPort: defaultUsenetPort(false) })}>
        <TextInput id="usenet-port" bind:value={port} mono placeholder={String(defaultUsenetPort(tls))} />
      </Field>

      <Toggle
        checked={tls}
        label={t('component.usenetServerSettings.useTls')}
        onchange={setTLS} />
      <p class="-mt-2 text-sm text-ink-muted">
        {t('component.usenetServerSettings.tlsHelp')}
      </p>

      <Field
        label={t('component.usenetServerSettings.username')}
        for="usenet-username"
        help={t('component.usenetServerSettings.fromYourProviderSAccountPageLeaveBlankForAServerThatNeedsNoLogin')}>
        <TextInput id="usenet-username" bind:value={username} mono placeholder={t('component.usenetServerSettings.user')} />
      </Field>

      <Field
        label={t('component.usenetServerSettings.password')}
        for="usenet-password"
        help={storedPassword
          ? t('component.usenetServerSettings.storedPasswordHelp')
          : t('component.usenetServerSettings.passwordStorageHelp')}>
        <TextInput
          id="usenet-password"
          bind:value={password}
          type="password"
          mono
          placeholder={storedPassword ? t('component.usenetServerSettings.unchanged') : '•••••'} />
      </Field>

      <div data-settings-advanced>
        <Field
          label={t('component.usenetServerSettings.connections')}
          for="usenet-connections"
          help={t('component.usenetServerSettings.neverSetThisAboveTheLimitOnYourPlanGoingOverGetsConnectionsRefusedRatherThanDownloadsSlowed')}>
          <TextInput
            id="usenet-connections"
            bind:value={maxConnections}
            mono
            placeholder={String(DEFAULT_USENET_MAX_CONNECTIONS)} />
        </Field>
      </div>

      <div data-settings-advanced>
        <Field
          label={t('component.usenetServerSettings.priority')}
          for="usenet-priority"
          help={t('component.usenetServerSettings.lowestWinsGiveABlockOrBackupAccountAHigherNumberSoItIsOnlyAskedForArticlesTheMainServerIsMissing')}>
          <TextInput
            id="usenet-priority"
            bind:value={priority}
            mono
            placeholder={String(DEFAULT_USENET_PRIORITY)} />
        </Field>
      </div>

      <Toggle checked={enabled} label={t('component.usenetServerSettings.enabled')} onchange={(next) => (enabled = next)} />

      {#if formError}
        <p class="text-sm text-danger">{formError}</p>
      {/if}
    </form>
    {#snippet footer()}

      <div class="mr-auto flex min-w-0 items-center gap-2">
        <Button variant="secondary" onclick={testForm} disabled={formTesting || saving}>
          {formTesting ? t('component.usenetServerSettings.testing') : t('component.usenetServerSettings.test')}
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
          {t('component.usenetServerSettings.remove')}
        </Button>
        <span class="mx-1 h-5 w-px shrink-0 bg-border"></span>
      {/if}
      <Button variant="ghost" onclick={closeForm} disabled={saving}>{t('component.usenetServerSettings.cancel')}</Button>
      <Button variant="primary" disabled={saving || !dirty || !formValid} onclick={save}>
        <Icon name="check" size={14} />
        {saving ? t('component.usenetServerSettings.saved') : !dirty ? t('component.usenetServerSettings.noChanges') : t('component.usenetServerSettings.save')}
      </Button>
    {/snippet}
  </Modal>
{/if}

{#if confirmingRemove}
  {@const target = confirmingRemove}
  <Modal title={t('component.usenetServerSettings.removeNewsServer')} width="max-w-lg" onclose={() => (confirmingRemove = null)}>
    <div class="flex flex-col gap-3 p-4">
      <p class="text-base text-ink">{target.name}</p>
      <p class="text-base text-ink-secondary">
        {t('component.usenetServerSettings.removalMessage')}
      </p>
    </div>

    {#snippet footer()}
      <Button variant="ghost" onclick={() => (confirmingRemove = null)}>{t('component.usenetServerSettings.cancel')}</Button>
      <Button variant="danger" disabled={busyID === target.id} onclick={remove}>{t('component.usenetServerSettings.remove')}</Button>
    {/snippet}
  </Modal>
{/if}
