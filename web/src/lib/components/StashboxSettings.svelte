<script lang="ts">
  /**
   * Settings → Metadata → Stash-box (PLAN Part 2 phase 8).
   *
   * One card per provider is this page's rule, and stash-box is the provider
   * that is configured more than once: each endpoint a household subscribes to
   * is its own instance, its own credential and its own id in a library's
   * provider chain. So this is the indexer screen's shape rather than
   * ProviderKeyCard's — a list of configured remotes with a name, a URL and a
   * write-only key — and it says the same thing the indexer rows do: an
   * endpoint that is configured but unreachable must report that HERE rather
   * than quietly identifying nothing on the next scan.
   *
   * Every route it calls lives under the adult mux and answers 404 while the
   * module is off, so the caller mounts this only for a session that already
   * sees the module. That is not a courtesy: a card that could enumerate a
   * household's catalogues on a server with the module off is the trace the
   * module promises not to leave.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import { STASHBOX_PRESETS, type StashboxInstance, type StashboxInstanceInput } from '../api/types';
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

  const SELECT_CLASS =
    'h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink ' +
    'focus:border-accent focus:outline-none disabled:opacity-50';

  /** The server's own reason, so the edit form can say why it has no URL field. */
  const ENDPOINT_IMMUTABLE =
    'The endpoint cannot be changed. Every item pinned to this instance carries a UUID only this ' +
    'box minted, so re-pointing it would have the next refresh overwrite those rows with whatever ' +
    'the new box holds under the same ids. Add an instance for the other box instead.';

  /** The result of the last test per instance id, so the row can say what happened. */
  type TestResult = { ok: boolean; message: string };

  let instances = $state<StashboxInstance[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let tests = $state<Record<number, TestResult>>({});
  let testingID = $state<number | null>(null);
  let busyID = $state<number | null>(null);

  let confirmingRemove = $state<StashboxInstance | null>(null);
  /**
   * The server's refusal, verbatim. It names the libraries and items that still
   * depend on the instance — the counts on the row are the same numbers — and
   * paraphrasing it would lose the one thing the user needs to act on.
   */
  let removeError = $state<string | null>(null);

  /** null = the form is closed; 0 = adding; otherwise the id being edited. */
  let editingID = $state<number | null>(null);
  let saving = $state(false);
  let formError = $state<string | null>(null);
  let formTest = $state<TestResult | null>(null);
  let formTesting = $state(false);

  let name = $state('');
  let endpoint = $state('');
  let apiKey = $state('');
  let hasAPIKey = $state(false);
  let clearAPIKey = $state(false);
  /** '' is Custom: the preset only fills the two fields, it does not bind them. */
  let presetID = $state('');

  async function load() {
    loading = true;
    try {
      instances = await api.listStashboxInstances();
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
    presetID = STASHBOX_PRESETS[0]?.id ?? '';
    name = STASHBOX_PRESETS[0]?.label ?? '';
    endpoint = STASHBOX_PRESETS[0]?.endpoint ?? '';
    apiKey = '';
    hasAPIKey = false;
    clearAPIKey = false;
  }

  function openEdit(instance: StashboxInstance) {
    editingID = instance.id;
    formError = null;
    formTest = null;
    presetID = '';
    name = instance.name;
    endpoint = instance.endpoint;
    apiKey = '';
    hasAPIKey = instance.has_api_key;
    clearAPIKey = false;
  }

  function closeForm() {
    editingID = null;
    formError = null;
    formTest = null;
  }

  /** A preset fills the form; both fields stay editable afterwards. */
  function pickPreset(id: string) {
    presetID = id;
    const preset = STASHBOX_PRESETS.find((p) => p.id === id);
    if (!preset) return;
    name = preset.label;
    endpoint = preset.endpoint;
  }

  let validationError = $derived.by(() => {
    if (name.trim() === '') return 'Give this stash-box a name.';
    if (endpoint.trim() === '') return 'Enter the stash-box GraphQL endpoint.';
    return null;
  });

  /**
   * The body for a create or an edit.
   *
   * An edit sends the endpoint EMPTY rather than repeating it: the server reads
   * a blank endpoint as "unchanged", and echoing the stored one back would make
   * the form the place a future typo could re-point an instance from.
   */
  function body(): StashboxInstanceInput {
    const out: StashboxInstanceInput = {
      name: name.trim(),
      endpoint: editingID === 0 ? endpoint.trim() : '',
    };
    if (apiKey.trim() !== '' || clearAPIKey) out.api_key = apiKey.trim();
    return out;
  }

  async function save() {
    if (saving) return;
    if (validationError) {
      formError = validationError;
      return;
    }
    saving = true;
    try {
      if (editingID === 0) {
        await api.createStashboxInstance(body());
        pushToast(`Added ${name.trim()}.`, 'success');
      } else if (editingID !== null) {
        await api.updateStashboxInstance(editingID, body());
        pushToast(`Saved ${name.trim()}.`, 'success');
      }
      closeForm();
      await load();
    } catch (err) {
      formError = errorText(err);
    } finally {
      saving = false;
    }
  }

  /**
   * Test what is in the form, against the body-shaped route: the add form has
   * no id to test yet, and on an edit the key being proved is the typed one
   * rather than the stored one.
   */
  async function testForm() {
    if (validationError) {
      formError = validationError;
      return;
    }
    formTesting = true;
    try {
      await api.testStashboxConfig({
        name: name.trim(),
        endpoint: endpoint.trim(),
        api_key: apiKey.trim(),
      });
      formTest = { ok: true, message: 'The stash-box answered.' };
    } catch (err) {
      formTest = { ok: false, message: errorText(err) };
    } finally {
      formTesting = false;
    }
  }

  async function test(instance: StashboxInstance) {
    testingID = instance.id;
    try {
      await api.testStashboxInstance(instance.id);
      tests = { ...tests, [instance.id]: { ok: true, message: 'Reachable' } };
    } catch (err) {
      tests = { ...tests, [instance.id]: { ok: false, message: errorText(err) } };
    } finally {
      testingID = null;
    }
  }

  async function remove() {
    const instance = confirmingRemove;
    if (!instance) return;
    busyID = instance.id;
    removeError = null;
    try {
      await api.deleteStashboxInstance(instance.id);
      confirmingRemove = null;
      pushToast(`Removed ${instance.name}.`, 'neutral');
      await load();
    } catch (err) {
      // Kept on the dialog rather than pushed as a toast: the refusal names
      // what to do next, and a toast is gone before it can be acted on.
      removeError = errorText(err);
    } finally {
      busyID = null;
    }
  }

  function usage(instance: StashboxInstance): string {
    const libraries = `${instance.library_count} ${instance.library_count === 1 ? 'library' : 'libraries'}`;
    const items = `${instance.item_count} ${instance.item_count === 1 ? 'item' : 'items'}`;
    return `Used by ${libraries} · ${items}`;
  }

  let rows = $derived(instances ?? []);
</script>

<SettingsCard
  title="Stash-box"
  description="Adult metadata: sites, scenes, performers and artwork. Each endpoint is its own provider — chain them per library in Libraries.">
  {#snippet action()}
    <Button variant="primary" size="sm" onclick={openAdd}>
      <Icon name="plus" size={14} />
      Add stash-box
    </Button>
  {/snippet}

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && instances === null}
    <div class="flex flex-col gap-2">
      {#each Array.from({ length: 2 }) as _, i (i)}
        <Skeleton class="h-14 w-full rounded-md" />
      {/each}
    </div>
  {:else if rows.length === 0}
    <EmptyState
      icon="link"
      title="No stash-box endpoint yet"
      message="Add StashDB, FansDB, PMV-Stash, ThePornDB or your own stash-box, then name it in an adult library's provider chain.">
      {#snippet action()}
        <Button variant="primary" onclick={openAdd}>Add stash-box</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <ul class="flex flex-col gap-2">
      {#each rows as instance (instance.id)}
        {@const result = tests[instance.id]}
        <li
          data-stashbox-row={instance.provider_id}
          class="flex flex-wrap items-center gap-3 rounded-md border border-border bg-surface px-3 py-3">
          <div class="min-w-0 flex-1">
            <p class="flex flex-wrap items-center gap-2">
              <span class="truncate text-base font-medium text-ink" title={instance.name}>
                {instance.name}
              </span>
              <!-- The badge is the whole of what may be said about the key:
                   it is write-only, so the value never leaves the server. -->
              <Badge tone={instance.has_api_key ? 'success' : 'neutral'}>
                {instance.has_api_key ? 'Key stored' : 'No key'}
              </Badge>
            </p>
            <p class="truncate font-mono text-xs text-ink-muted" title={instance.endpoint}>
              {instance.endpoint}
            </p>
            <p class="text-sm text-ink-secondary">{usage(instance)}</p>
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
              disabled={testingID === instance.id}
              onclick={() => test(instance)}>
              {testingID === instance.id ? 'Testing…' : 'Test'}
            </Button>
            <Button variant="ghost" size="sm" onclick={() => openEdit(instance)}>Edit</Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={busyID === instance.id}
              title="Remove {instance.name}"
              onclick={() => {
                removeError = null;
                confirmingRemove = instance;
              }}>
              <Icon name="trash" size={14} />
              <span class="sr-only">Remove {instance.name}</span>
            </Button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</SettingsCard>

{#if editingID !== null}
  <Modal
    title={editingID === 0 ? 'Add stash-box' : 'Edit stash-box'}
    width="max-w-xl"
    onclose={closeForm}>
    <form
      class="flex flex-col gap-4 p-4"
      onsubmit={(event) => {
        event.preventDefault();
        void save();
      }}>
      {#if editingID === 0}
        <Field
          label="Stash-box"
          for="stashbox-preset"
          help="Fills the two fields below. Both stay editable — pick Custom for a box that is not listed.">
          <select
            id="stashbox-preset"
            value={presetID}
            onchange={(event) => pickPreset(event.currentTarget.value)}
            class={SELECT_CLASS}>
            {#each STASHBOX_PRESETS as preset (preset.id)}
              <option value={preset.id}>{preset.label} — {preset.endpoint}</option>
            {/each}
            <option value="">Custom stash-box…</option>
          </select>
        </Field>
      {/if}

      <Field label="Name" for="stashbox-name" help="How this box is labelled in a library's provider chain.">
        <TextInput id="stashbox-name" bind:value={name} placeholder="StashDB" />
      </Field>

      {#if editingID === 0}
        <Field
          label="Endpoint"
          for="stashbox-endpoint"
          help="The box's GraphQL endpoint — an absolute http(s) URL.">
          <TextInput id="stashbox-endpoint" bind:value={endpoint} mono placeholder="https://stashdb.org/graphql" />
        </Field>
      {:else}
        <!-- No field at all rather than a disabled one: the server refuses an
             endpoint change, and an input that cannot be used is an offer the
             screen cannot keep. -->
        <Field label="Endpoint" help={ENDPOINT_IMMUTABLE}>
          <p class="truncate font-mono text-sm text-ink-secondary" title={endpoint}>{endpoint}</p>
        </Field>
      {/if}

      <Field
        label="API key"
        for="stashbox-api-key"
        help="Stored in the database, never in caravan.yaml and never logged.">
        <div class="flex flex-col gap-2">
          <TextInput
            id="stashbox-api-key"
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

      {#if formTest}
        <p class="text-sm {formTest.ok ? 'text-success' : 'text-danger'}">
          {formTest.ok ? '✓ ' : '✕ '}{formTest.message}
        </p>
      {/if}
      {#if formError}
        <p class="text-sm text-danger">{formError}</p>
      {/if}
    </form>

    {#snippet footer()}
      <Button variant="secondary" disabled={formTesting || saving} onclick={testForm}>
        {formTesting ? 'Testing…' : 'Test'}
      </Button>
      <span class="flex-1"></span>
      <Button variant="ghost" onclick={closeForm} disabled={saving}>Cancel</Button>
      <Button variant="primary" disabled={saving || validationError !== null} onclick={save}>
        <Icon name="check" size={14} />
        {saving ? 'Saving…' : 'Save'}
      </Button>
    {/snippet}
  </Modal>
{/if}

{#if confirmingRemove}
  {@const target = confirmingRemove}
  <Modal title="Remove stash-box" width="max-w-lg" onclose={() => (confirmingRemove = null)}>
    <div class="flex flex-col gap-3 p-4">
      <p class="text-base text-ink">{target.name}</p>
      <p class="text-base text-ink-secondary">
        Caravan stops asking this box. Nothing already imported is deleted — the sites, the scenes
        and the files stay where they are, and items pinned to this box simply stop refreshing.
      </p>
      <p class="text-sm text-ink-secondary">{usage(target)}</p>
      {#if removeError}
        <p class="text-sm text-danger" role="alert">{removeError}</p>
      {/if}
    </div>

    {#snippet footer()}
      <Button variant="ghost" onclick={() => (confirmingRemove = null)}>Cancel</Button>
      <Button variant="danger" disabled={busyID === target.id} onclick={remove}>Remove</Button>
    {/snippet}
  </Modal>
{/if}
