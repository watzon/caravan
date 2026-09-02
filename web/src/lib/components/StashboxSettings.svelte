<script lang="ts">
  import { useI18n } from '../i18n.svelte';
  /**
   * Settings → Metadata → Stash-box.
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
   * Every route it calls is admin-only. The card is mounted for any admin
   * session, including one that has no adult library yet: that is how the first
   * endpoint gets added before Add library can point at it.
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
  const { t, tp } = useI18n();

  /** The server's own reason, so the edit form can say why it has no URL field. */
  const ENDPOINT_IMMUTABLE = t('component.stashboxSettings.endpointImmutable');

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
    if (name.trim() === '') return t('component.stashboxSettings.nameRequired');
    if (endpoint.trim() === '') return t('component.stashboxSettings.endpointRequired');
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
        pushToast(t('component.stashboxSettings.added', { name: name.trim() }), 'success');
      } else if (editingID !== null) {
        await api.updateStashboxInstance(editingID, body());
        pushToast(t('component.stashboxSettings.saved', { name: name.trim() }), 'success');
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
      formTest = { ok: true, message: t('component.stashboxSettings.answered') };
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
      tests = { ...tests, [instance.id]: { ok: true, message: t('component.stashboxSettings.reachable') } };
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
      pushToast(t('component.stashboxSettings.removed', { name: instance.name }), 'neutral');
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
    return t('component.stashboxSettings.usage', {
      libraries: tp('component.stashboxSettings.library', instance.library_count),
      items: tp('component.stashboxSettings.item', instance.item_count),
    });
  }

  let rows = $derived(instances ?? []);

</script>

<SettingsCard
  title={t('component.stashboxSettings.stashBox')}
  description={t('component.stashboxSettings.adultMetadataSitesScenesPerformersAndArtworkEachEndpointIsItsOwnProviderChainThemPerLibraryInLibraries')}>
  {#snippet action()}
    <Button variant="primary" size="sm" onclick={openAdd}>
      <Icon name="plus" size={14} />
      {t('component.stashboxSettings.addStashBox')}
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
      title={t('component.stashboxSettings.noStashBoxEndpointYet')}
      message={t('component.stashboxSettings.addStashdbFansdbPmvStashTheporndbOrYourOwnStashBoxThenNameItInAnAdultLibrarySProviderChain')}>
      {#snippet action()}
        <Button variant="primary" onclick={openAdd}>{t('component.stashboxSettings.addStashBox')}</Button>
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
                {instance.has_api_key ? t('component.stashboxSettings.keyStored') : t('component.stashboxSettings.noKey')}
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
            <Button variant="ghost" size="sm" onclick={() => openEdit(instance)}>{t('component.stashboxSettings.edit')}</Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={busyID === instance.id}
              title={t('component.stashboxSettings.removeTitle', { name: instance.name })}
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
    title={editingID === 0 ? t('component.stashboxSettings.addModal') : t('component.stashboxSettings.editModal')}
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
          label={t('component.stashboxSettings.stashBox')}
          for="stashbox-preset"
          help={t('component.stashboxSettings.fillsTheTwoFieldsBelowBothStayEditablePickCustomForABoxThatIsNotListed')}>
          <select
            id="stashbox-preset"
            value={presetID}
            onchange={(event) => pickPreset(event.currentTarget.value)}
            class={SELECT_CLASS}>
            {#each STASHBOX_PRESETS as preset (preset.id)}
              <option value={preset.id}>{preset.label} — {preset.endpoint}</option>
            {/each}
            <option value="">{t('component.stashboxSettings.customStashBox')}</option>
          </select>
        </Field>
      {/if}

      <Field label={t('component.stashboxSettings.name')} for="stashbox-name" help={t('component.stashboxSettings.howThisBoxIsLabelledInALibrarySProviderChain')}>
        <TextInput id="stashbox-name" bind:value={name} placeholder={t('component.stashboxSettings.stashdb')} />
      </Field>

      {#if editingID === 0}
        <Field
          label={t('component.stashboxSettings.endpoint')}
          for="stashbox-endpoint"
          help={t('component.stashboxSettings.theBoxSGraphqlEndpointAnAbsoluteHttpSUrl')}>
          <TextInput id="stashbox-endpoint" bind:value={endpoint} mono placeholder={t('component.stashboxSettings.httpsStashdbOrgGraphql')} />
        </Field>
      {:else}
        <!-- No field at all rather than a disabled one: the server refuses an
             endpoint change, and an input that cannot be used is an offer the
             screen cannot keep. -->
        <Field label={t('component.stashboxSettings.endpoint')} help={ENDPOINT_IMMUTABLE}>
          <p class="truncate font-mono text-sm text-ink-secondary" title={endpoint}>{endpoint}</p>
        </Field>
      {/if}

      <Field
        label={t('component.stashboxSettings.apiKey')}
        for="stashbox-api-key"
        help={t('component.stashboxSettings.storedInTheDatabaseNeverInCaravanYamlAndNeverLogged')}>
        <div class="flex flex-col gap-2">
          <TextInput
            id="stashbox-api-key"
            bind:value={apiKey}
            type="password"
            mono
            placeholder="•••••"
            oninput={() => (clearAPIKey = false)} />
          {#if hasAPIKey}
            <p class="text-sm text-ink-secondary">{t('component.stashboxSettings.aKeyIsStoredLeaveBlankToKeepIt')}</p>
            <Button variant="secondary" size="sm" onclick={() => (clearAPIKey = true)}>
              {t('component.stashboxSettings.clearApiKey')}
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
      <Button variant="ghost" onclick={closeForm} disabled={saving}>{t('component.stashboxSettings.cancel')}</Button>
      <Button variant="primary" disabled={saving || validationError !== null} onclick={save}>
        <Icon name="check" size={14} />
        {saving ? 'Saving…' : 'Save'}
      </Button>
    {/snippet}
  </Modal>
{/if}

{#if confirmingRemove}
  {@const target = confirmingRemove}
  <Modal title={t('component.stashboxSettings.removeStashBox')} width="max-w-lg" onclose={() => (confirmingRemove = null)}>
    <div class="flex flex-col gap-3 p-4">
      <p class="text-base text-ink">{target.name}</p>
      <p class="text-base text-ink-secondary">
        {t('component.stashboxSettings.removalMessage')}
      </p>
      <p class="text-sm text-ink-secondary">{usage(target)}</p>
      {#if removeError}
        <p class="text-sm text-danger" role="alert">{removeError}</p>
      {/if}
    </div>

    {#snippet footer()}
      <Button variant="ghost" onclick={() => (confirmingRemove = null)}>{t('component.stashboxSettings.cancel')}</Button>
      <Button variant="danger" disabled={busyID === target.id} onclick={remove}>{t('component.stashboxSettings.remove')}</Button>
    {/snippet}
  </Modal>
{/if}
