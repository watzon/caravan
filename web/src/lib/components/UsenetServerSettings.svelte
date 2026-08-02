<script lang="ts">
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
  import Skeleton from './Skeleton.svelte';
  import TextInput from './TextInput.svelte';
  import Toggle from './Toggle.svelte';

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
  }

  function closeForm() {
    editingID = null;
    formError = null;
    formTest = null;
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
    const problem = validateUsenetServer({
      name,
      host,
      port,
      username,
      password,
      maxConnections,
      hasStoredPassword: storedPassword && username.trim() !== '',
    });
    formError = problem;
    return problem === null;
  }

  async function save() {
    if (!validate()) return;
    const body = formBody();

    saving = true;
    try {
      if (editingID === 0) {
        await api.addUsenetServer(body);
        pushToast(`Added ${body.name}.`, 'success');
      } else if (editingID !== null) {
        await api.updateUsenetServer(editingID, body);
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
      formTest = { ok: true, message: 'Connected' };
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
      tests = { ...tests, [server.id]: { ok: true, message: 'Connected' } };
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
      pushToast(`Removed ${server.name}.`, 'neutral');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busyID = null;
    }
  }

  let rows = $derived(servers ?? []);
</script>

<section class="flex flex-col gap-4">
  <div class="flex flex-wrap items-center gap-3">
    <div class="ml-auto flex items-center gap-2">
      <Button variant="secondary" onclick={load}>
        <Icon name="refresh" size={14} />
        Refresh
      </Button>
      <Button variant="primary" onclick={openAdd} disabled={editingID === 0}>
        <Icon name="plus" size={14} />
        Add server
      </Button>
    </div>
  </div>

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && servers === null}
    <div class="flex flex-col gap-2">
      {#each Array.from({ length: 2 }) as _, i (i)}
        <Skeleton class="h-14 w-full rounded-md" />
      {/each}
    </div>
  {:else if rows.length === 0 && editingID === null}
    <EmptyState
      icon="link"
      title="No news servers yet"
      message="Caravan's built-in engine reads Usenet articles straight from your provider. Add the server details from your provider's account page to download Usenet releases.">
      {#snippet action()}
        <Button variant="primary" onclick={openAdd}>Add server</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <ul class="flex flex-col gap-2">
      {#each rows as server (server.id)}
        {@const result = tests[server.id]}
        <li class="flex flex-wrap items-center gap-3 rounded-md border border-border bg-surface px-3 py-3">
          <span
            class="size-2 shrink-0 rounded-full {server.enabled ? 'bg-success' : 'bg-ink-muted'}">
          </span>
          <span class="sr-only">{server.enabled ? 'Enabled' : 'Disabled'}</span>

          <div class="min-w-0 flex-1">
            <p class="flex flex-wrap items-center gap-2">
              <span class="truncate text-base font-medium text-ink">{server.name}</span>
              <Badge mono tone={server.tls ? 'accent' : 'warning'}>
                {server.tls ? 'TLS' : 'Plaintext'}
              </Badge>
              <Badge mono tone="neutral">
                {server.max_connections} conn
              </Badge>
              <Badge mono tone="neutral">Priority {server.priority}</Badge>
              {#if !server.enabled}
                <Badge tone="neutral">Disabled</Badge>
              {/if}
            </p>
            <p class="truncate font-mono text-xs text-ink-muted" title="{server.host}:{server.port}">
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
              {testingID === server.id ? 'Testing…' : 'Test'}
            </Button>
            <Button variant="ghost" size="sm" onclick={() => openEdit(server)}>Edit</Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={busyID === server.id}
              onclick={() => (confirmingRemove = server)}>
              <Icon name="trash" size={14} />
              <span class="sr-only">Remove {server.name}</span>
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
        {editingID === 0 ? 'Add news server' : 'Edit news server'}
      </h3>

      <Field label="Name" for="usenet-name" help="How this server is labelled in errors and logs.">
        <TextInput id="usenet-name" bind:value={name} placeholder="Eweka" />
      </Field>

      <Field
        label="Hostname"
        for="usenet-host"
        help="Just the hostname from your provider — no http:// and no path.">
        <TextInput id="usenet-host" bind:value={host} mono placeholder="news.eweka.nl" />
      </Field>

      <Field
        label="Port"
        for="usenet-port"
        help="Leave the default unless your provider says otherwise: {defaultUsenetPort(
          true,
        )} with TLS, {defaultUsenetPort(false)} without.">
        <TextInput id="usenet-port" bind:value={port} mono placeholder={String(defaultUsenetPort(tls))} />
      </Field>

      <Toggle
        checked={tls}
        label="Use TLS"
        onchange={setTLS} />
      <p class="-mt-2 text-sm text-ink-muted">
        Encrypts the connection, including the password below. Leave this on unless your
        provider genuinely has no TLS port.
      </p>

      <Field
        label="Username"
        for="usenet-username"
        help="From your provider's account page. Leave blank for a server that needs no login.">
        <TextInput id="usenet-username" bind:value={username} mono placeholder="user" />
      </Field>

      <Field
        label="Password"
        for="usenet-password"
        help={storedPassword
          ? 'A password is stored. Leave this blank to keep it — it is never sent back to the browser.'
          : 'Stored in the database, never in caravan.yaml and never logged.'}>
        <TextInput
          id="usenet-password"
          bind:value={password}
          type="password"
          mono
          placeholder={storedPassword ? 'Unchanged' : '•••••'} />
      </Field>

      <Field
        label="Connections"
        for="usenet-connections"
        help="Never set this above the limit on your plan: going over gets connections refused rather than downloads slowed.">
        <TextInput
          id="usenet-connections"
          bind:value={maxConnections}
          mono
          placeholder={String(DEFAULT_USENET_MAX_CONNECTIONS)} />
      </Field>

      <Field
        label="Priority"
        for="usenet-priority"
        help="Lowest wins. Give a block or backup account a higher number so it is only asked for articles the main server is missing.">
        <TextInput
          id="usenet-priority"
          bind:value={priority}
          mono
          placeholder={String(DEFAULT_USENET_PRIORITY)} />
      </Field>

      <Toggle checked={enabled} label="Enabled" onchange={(next) => (enabled = next)} />

      {#if formError}
        <p class="text-sm text-danger">{formError}</p>
      {/if}
      {#if formTest}
        <p class="text-sm {formTest.ok ? 'text-success' : 'text-danger'}">
          {formTest.ok ? '✓ ' : '✕ '}{formTest.message}
        </p>
      {/if}

      <div class="flex gap-2">
        <Button variant="primary" type="submit" disabled={saving}>
          <Icon name="check" size={14} />
          {saving ? 'Saving…' : 'Save'}
        </Button>
        <Button variant="secondary" onclick={testForm} disabled={formTesting || saving}>
          {formTesting ? 'Testing…' : 'Test'}
        </Button>
        <Button variant="ghost" onclick={closeForm} disabled={saving}>Cancel</Button>
      </div>
    </form>
  {/if}
</section>

{#if confirmingRemove}
  {@const target = confirmingRemove}
  <Modal title="Remove news server" width="max-w-lg" onclose={() => (confirmingRemove = null)}>
    <div class="flex flex-col gap-3 p-4">
      <p class="text-base text-ink">{target.name}</p>
      <p class="text-base text-ink-secondary">
        Caravan stops downloading articles from this server. Nothing already downloaded or
        imported is affected — only the configuration goes away. If it is the only server
        configured, Usenet grabs will have nowhere to download from.
      </p>
    </div>

    {#snippet footer()}
      <Button variant="ghost" onclick={() => (confirmingRemove = null)}>Cancel</Button>
      <Button variant="danger" disabled={busyID === target.id} onclick={remove}>Remove</Button>
    {/snippet}
  </Modal>
{/if}
