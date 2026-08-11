<script lang="ts">
  import { useI18n } from '../i18n.svelte';
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

  let nameError = $derived(name.trim() === '' ? t('component.notificationSettings.nameRequired') : null);
  let urlError = $derived.by(() => {
    if (url.trim() === '') return editing ? null : t('component.notificationSettings.urlRequired');
    try {
      const parsed = new URL(url.trim());
      if ((parsed.protocol !== 'http:' && parsed.protocol !== 'https:') || !parsed.host) {
        return t('component.notificationSettings.urlRequired');
      }
      if (parsed.username || parsed.password) return t('component.notificationSettings.urlUserInfo');
      return null;
    } catch {
      return t('component.notificationSettings.urlRequired');
    }
  });
  let eventsError = $derived(!onGrab && !onImport && !onHealth ? t('component.notificationSettings.eventRequired') : null);
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
      pushToast(wasEditing ? t('component.notificationSettings.webhookUpdated') : t('component.notificationSettings.webhookAdded'), 'success');
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
      pushToast(t('component.notificationSettings.testDelivered', { name: webhook.name }), 'success');
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
      pushToast(t('component.notificationSettings.webhookRemoved'), 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      deletingBusy = false;
    }
  }

  const { t, tp } = useI18n();
</script>

<section id="notifications" class="flex max-w-5xl flex-col gap-5" aria-labelledby="notifications-heading">
  <div class="flex flex-wrap items-start gap-3">
    <div class="min-w-0 flex-1">
      <h2 id="notifications-heading" class="text-lg font-semibold text-ink">{t('component.notificationSettings.notificationWebhooks')}</h2>
      <p class="mt-1 text-sm text-ink-secondary">{t('component.notificationSettings.sendSelectedActivityToAnotherServiceNewWebhooksStartAtTheCurrentEventSoOldHistoryIsNeverReplayed')}</p>
    </div>
    <Button variant="primary" onclick={() => openForm(null)}>{t('component.notificationSettings.addWebhook')}</Button>
  </div>

  {#if error && webhooks === null}
    <LoadError message={error} onretry={load} />
  {:else if loading && webhooks === null}
    <div class="flex flex-col gap-2">{#each Array.from({ length: 2 }) as _, i (i)}<Skeleton class="h-24 w-full rounded-md" />{/each}</div>
  {:else if (webhooks ?? []).length === 0}
    <EmptyState icon="link" title={t('component.notificationSettings.noNotificationWebhooks')} message={t('component.notificationSettings.addOneToSendGrabsImportsOrHealthProblemsToAnotherService')} />
  {:else}
    <ul class="overflow-hidden rounded-md border border-border bg-surface">
      {#each webhooks ?? [] as webhook (webhook.id)}
        <li class="flex flex-wrap items-start gap-4 border-b border-border px-4 py-4 last:border-b-0">
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <p class="font-medium text-ink">{webhook.name}</p>
              <Badge tone={webhook.enabled ? 'success' : 'neutral'}>{webhook.enabled ? t('component.notificationSettings.enabled') : t('component.notificationSettings.disabled')}</Badge>
            </div>
            <p class="mt-1 text-sm text-ink-secondary">{webhook.has_url ? t('component.notificationSettings.endpointConfigured') : t('component.notificationSettings.noEndpointConfigured')}</p>
            <div class="mt-2 flex flex-wrap gap-1" aria-label={t('component.notificationSettings.webhookEvents', { name: webhook.name })}>
              {#if webhook.on_grab}<Badge tone="neutral">{t('component.notificationSettings.grabbed')}</Badge>{/if}
              {#if webhook.on_import}<Badge tone="neutral">{t('component.notificationSettings.imported')}</Badge>{/if}
              {#if webhook.on_health}<Badge tone="neutral">{t('component.notificationSettings.health')}</Badge>{/if}
            </div>
          </div>
          <div class="flex flex-wrap gap-2">
            <Button size="sm" variant="secondary" disabled={testingID !== null} onclick={() => test(webhook)}>
              {testingID === webhook.id ? t('component.notificationSettings.testing') : t('component.notificationSettings.test')}
            </Button>
            <Button size="sm" variant="secondary" onclick={() => openForm(webhook)}>{t('component.notificationSettings.edit')}</Button>
            <Button size="sm" variant="ghost" onclick={() => (deleting = webhook)}>{t('component.notificationSettings.remove')}</Button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}

  <SettingsCard title={t('component.notificationSettings.delivery')} description={t('component.notificationSettings.caravanChecksForNewNotificationEventsEveryFiveMinutesByDefaultChangeTheNotificationDeliveryCadenceUnderTasks')}>
    <p class="text-sm text-ink-secondary">{t('component.notificationSettings.aFailedEndpointIsRetriedWithoutSkippingTheEventLaterEventsWaitBehindItSoDeliveryOrderStaysIntact')}</p>
  </SettingsCard>
</section>

{#if formOpen}
  <Modal title={editing ? t('component.notificationSettings.editWebhook') : t('component.notificationSettings.addWebhookModal')} width="max-w-xl" dirty={dirty} onclose={closeForm}>
    <form class="flex flex-col gap-5 p-4" onsubmit={(event) => { event.preventDefault(); save(); }}>
      <Field label={t('component.notificationSettings.name')} for="notification-name" error={nameError}>
        <TextInput id="notification-name" bind:value={name} autofocus placeholder={t('component.notificationSettings.homeAutomation')} />
      </Field>
      <Field label={t('component.notificationSettings.webhookUrl')} for="notification-url" error={urlError} help={editing ? t('component.notificationSettings.webhookUrlHelpEdit') : t('component.notificationSettings.webhookUrlHelpAdd')}>
        <TextInput id="notification-url" bind:value={url} mono placeholder={t('component.notificationSettings.httpsExampleComHooksCaravan')} />
      </Field>
      <Field label={t('component.notificationSettings.events')} error={eventsError} help={t('component.notificationSettings.healthIncludesWarningsAndErrorsFromAnyCaravanSubsystem')}>
        <div class="grid gap-2 sm:grid-cols-3">
          <Toggle checked={onGrab} label={t('component.notificationSettings.grabbed')} onchange={(value) => (onGrab = value)} />
          <Toggle checked={onImport} label={t('component.notificationSettings.imported')} onchange={(value) => (onImport = value)} />
          <Toggle checked={onHealth} label={t('component.notificationSettings.healthProblems')} onchange={(value) => (onHealth = value)} />
        </div>
      </Field>
      <Toggle checked={enabled} label={t('component.notificationSettings.enabled')} onchange={(value) => (enabled = value)} />
    </form>
    {#snippet footer()}
      <div class="flex w-full flex-wrap justify-end gap-2">
        <Button variant="secondary" disabled={saving} onclick={closeForm}>{t('component.notificationSettings.cancel')}</Button>
        <Button variant="primary" disabled={saving || !valid || !dirty} onclick={save}>
          {saving ? t('component.notificationSettings.saving') : !valid ? t('component.notificationSettings.fixErrors') : !dirty ? t('component.notificationSettings.noChanges') : t('component.notificationSettings.save')}
        </Button>
      </div>
    {/snippet}
  </Modal>
{/if}

{#if deleting}
  <Modal title={t('component.notificationSettings.removeNotificationWebhook')} width="max-w-md" onclose={() => (deleting = null)}>
    <div class="px-4 py-4 text-base text-ink-secondary">
      {t('component.notificationSettings.removeWebhookMessage', { name: deleting.name })}
    </div>
    {#snippet footer()}
      <div class="flex w-full flex-wrap justify-end gap-2">
        <Button variant="secondary" disabled={deletingBusy} onclick={() => (deleting = null)}>{t('component.notificationSettings.cancel')}</Button>
        <Button variant="danger" disabled={deletingBusy} onclick={remove}>{deletingBusy ? t('component.notificationSettings.removing') : t('component.notificationSettings.remove')}</Button>
      </div>
    {/snippet}
  </Modal>
{/if}
