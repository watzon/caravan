<script lang="ts">
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { NotificationWebhook, NotificationWebhookInput } from '../api/types';
  import { pushToast } from '../state/toast.svelte';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import EmptyState from './EmptyState.svelte';
  import Field from './Field.svelte';
  import LoadError from './LoadError.svelte';
  import Modal from './Modal.svelte';
  import SettingsCard from './SettingsCard.svelte';
  import Skeleton from './Skeleton.svelte';
  import TextInput from './TextInput.svelte';
  import Toggle from './Toggle.svelte';

  let webhooks = $state<NotificationWebhook[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let editing = $state<NotificationWebhook | null>(null);
  let formOpen = $state(false);
  let saving = $state(false);
  let testingID = $state<number | null>(null);
  let deleting = $state<NotificationWebhook | null>(null);
  let deletingBusy = $state(false);

  let name = $state('');
  let url = $state('');
  let onGrab = $state(true);
  let onImport = $state(true);
  let onHealth = $state(true);
  let enabled = $state(true);
  let initial = $state('');

  function draft(): NotificationWebhookInput {
    const input: NotificationWebhookInput = {
      name: name.trim(),
      on_grab: onGrab,
      on_import: onImport,
      on_health: onHealth,
      enabled,
    };
    if (url.trim() !== '') input.url = url.trim();
    return input;
  }

  let nameError = $derived(name.trim() === '' ? 'Enter a name.' : null);
  let urlError = $derived.by(() => {
    if (url.trim() === '') return editing ? null : 'Enter an absolute HTTP or HTTPS URL.';
    try {
      const parsed = new URL(url.trim());
      if ((parsed.protocol !== 'http:' && parsed.protocol !== 'https:') || !parsed.host) {
        return 'Enter an absolute HTTP or HTTPS URL.';
      }
      if (parsed.username || parsed.password) return 'URL user information is not allowed.';
      return null;
    } catch {
      return 'Enter an absolute HTTP or HTTPS URL.';
    }
  });
  let eventsError = $derived(!onGrab && !onImport && !onHealth ? 'Select at least one event.' : null);
  let dirty = $derived(formOpen && JSON.stringify(draft()) !== initial);
  let valid = $derived(nameError === null && urlError === null && eventsError === null);

  async function load() {
    loading = true;
    try {
      webhooks = await api.listNotificationWebhooks();
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  function openForm(webhook: NotificationWebhook | null) {
    editing = webhook;
    name = webhook?.name ?? '';
    url = '';
    onGrab = webhook?.on_grab ?? true;
    onImport = webhook?.on_import ?? true;
    onHealth = webhook?.on_health ?? true;
    enabled = webhook?.enabled ?? true;
    initial = JSON.stringify(draft());
    formOpen = true;
  }

  function closeForm() {
    formOpen = false;
    editing = null;
  }

  async function save() {
    if (!valid || !dirty || saving) return;
    const wasEditing = editing !== null;
    const editingID = editing?.id;
    saving = true;
    try {
      const saved = wasEditing
        ? await api.updateNotificationWebhook(editingID!, draft())
        : await api.addNotificationWebhook(draft());
      webhooks = wasEditing
        ? (webhooks ?? []).map((item) => item.id === saved.id ? saved : item)
        : [...(webhooks ?? []), saved];
      closeForm();
      pushToast(wasEditing ? 'Notification webhook updated.' : 'Notification webhook added.', 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      saving = false;
    }
  }

  async function test(webhook: NotificationWebhook) {
    if (testingID !== null) return;
    testingID = webhook.id;
    try {
      await api.testNotificationWebhook(webhook.id);
      pushToast(`Test delivered to ${webhook.name}.`, 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      testingID = null;
    }
  }

  async function remove() {
    if (!deleting || deletingBusy) return;
    const webhook = deleting;
    deletingBusy = true;
    try {
      await api.deleteNotificationWebhook(webhook.id);
      webhooks = (webhooks ?? []).filter((item) => item.id !== webhook.id);
      deleting = null;
      pushToast('Notification webhook removed.', 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      deletingBusy = false;
    }
  }
</script>

<section id="notifications" class="flex max-w-5xl flex-col gap-5" aria-labelledby="notifications-heading">
  <div class="flex flex-wrap items-start gap-3">
    <div class="min-w-0 flex-1">
      <h2 id="notifications-heading" class="text-lg font-semibold text-ink">Notification webhooks</h2>
      <p class="mt-1 text-sm text-ink-secondary">Send selected activity to another service. New webhooks start at the current event, so old history is never replayed.</p>
    </div>
    <Button variant="primary" onclick={() => openForm(null)}>Add webhook</Button>
  </div>

  {#if error && webhooks === null}
    <LoadError message={error} onretry={load} />
  {:else if loading && webhooks === null}
    <div class="flex flex-col gap-2">{#each Array.from({ length: 2 }) as _, i (i)}<Skeleton class="h-24 w-full rounded-md" />{/each}</div>
  {:else if (webhooks ?? []).length === 0}
    <EmptyState icon="link" title="No notification webhooks" message="Add one to send grabs, imports, or health problems to another service." />
  {:else}
    <ul class="overflow-hidden rounded-md border border-border bg-surface">
      {#each webhooks ?? [] as webhook (webhook.id)}
        <li class="flex flex-wrap items-start gap-4 border-b border-border px-4 py-4 last:border-b-0">
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <p class="font-medium text-ink">{webhook.name}</p>
              <Badge tone={webhook.enabled ? 'success' : 'neutral'}>{webhook.enabled ? 'Enabled' : 'Disabled'}</Badge>
            </div>
            <p class="mt-1 text-sm text-ink-secondary">{webhook.has_url ? 'Endpoint configured' : 'No endpoint configured'}</p>
            <div class="mt-2 flex flex-wrap gap-1" aria-label={`${webhook.name} events`}>
              {#if webhook.on_grab}<Badge tone="neutral">Grabbed</Badge>{/if}
              {#if webhook.on_import}<Badge tone="neutral">Imported</Badge>{/if}
              {#if webhook.on_health}<Badge tone="neutral">Health</Badge>{/if}
            </div>
          </div>
          <div class="flex flex-wrap gap-2">
            <Button size="sm" variant="secondary" disabled={testingID !== null} onclick={() => test(webhook)}>
              {testingID === webhook.id ? 'Testing…' : 'Test'}
            </Button>
            <Button size="sm" variant="secondary" onclick={() => openForm(webhook)}>Edit</Button>
            <Button size="sm" variant="ghost" onclick={() => (deleting = webhook)}>Remove</Button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}

  <SettingsCard title="Delivery" description="Caravan checks for new notification events every five minutes by default. Change the Notification delivery cadence under Tasks.">
    <p class="text-sm text-ink-secondary">A failed endpoint is retried without skipping the event. Later events wait behind it so delivery order stays intact.</p>
  </SettingsCard>
</section>

{#if formOpen}
  <Modal title={editing ? 'Edit notification webhook' : 'Add notification webhook'} width="max-w-xl" dirty={dirty} onclose={closeForm}>
    <form class="flex flex-col gap-5 p-4" onsubmit={(event) => { event.preventDefault(); save(); }}>
      <Field label="Name" for="notification-name" error={nameError}>
        <TextInput id="notification-name" bind:value={name} autofocus placeholder="Home automation" />
      </Field>
      <Field label="Webhook URL" for="notification-url" error={urlError} help={editing ? 'Write-only. Leave blank to keep the configured endpoint.' : 'An absolute HTTP or HTTPS endpoint. It will not be shown again.'}>
        <TextInput id="notification-url" bind:value={url} mono placeholder="https://example.com/hooks/caravan" />
      </Field>
      <Field label="Events" error={eventsError} help="Health includes warnings and errors from any Caravan subsystem.">
        <div class="grid gap-2 sm:grid-cols-3">
          <Toggle checked={onGrab} label="Grabbed" onchange={(value) => (onGrab = value)} />
          <Toggle checked={onImport} label="Imported" onchange={(value) => (onImport = value)} />
          <Toggle checked={onHealth} label="Health problems" onchange={(value) => (onHealth = value)} />
        </div>
      </Field>
      <Toggle checked={enabled} label="Enabled" onchange={(value) => (enabled = value)} />
    </form>
    {#snippet footer()}
      <div class="flex w-full flex-wrap justify-end gap-2">
        <Button variant="secondary" disabled={saving} onclick={closeForm}>Cancel</Button>
        <Button variant="primary" disabled={saving || !valid || !dirty} onclick={save}>
          {saving ? 'Saving…' : !valid ? 'Fix errors' : !dirty ? 'No changes' : 'Save'}
        </Button>
      </div>
    {/snippet}
  </Modal>
{/if}

{#if deleting}
  <Modal title="Remove notification webhook?" width="max-w-md" onclose={() => (deleting = null)}>
    <div class="px-4 py-4 text-base text-ink-secondary">
      Caravan will stop sending events to <span class="font-medium text-ink">{deleting.name}</span>. This cannot be undone.
    </div>
    {#snippet footer()}
      <div class="flex w-full flex-wrap justify-end gap-2">
        <Button variant="secondary" disabled={deletingBusy} onclick={() => (deleting = null)}>Cancel</Button>
        <Button variant="danger" disabled={deletingBusy} onclick={remove}>{deletingBusy ? 'Removing…' : 'Remove'}</Button>
      </div>
    {/snippet}
  </Modal>
{/if}
