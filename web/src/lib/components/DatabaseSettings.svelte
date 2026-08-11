<script lang="ts">
  import { api, endpoints, errorText } from '../api/client';
  import { useI18n } from '../i18n.svelte';
  import { pushToast } from '../state/toast.svelte';
  import Banner from './Banner.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import Modal from './Modal.svelte';
  import SettingsCard from './SettingsCard.svelte';

  const { t } = useI18n();

  let fileInput: HTMLInputElement;
  let restoreFile = $state<File | null>(null);
  let restoring = $state(false);
  let staged = $state(false);

  function chooseRestore(event: Event) {
    restoreFile = event.currentTarget instanceof HTMLInputElement
      ? event.currentTarget.files?.[0] ?? null
      : null;
  }

  function closeRestore() {
    if (restoring) return;
    restoreFile = null;
    if (fileInput) fileInput.value = '';
  }

  async function restore() {
    if (!restoreFile || restoring) return;
    restoring = true;
    try {
      await api.restoreBackup(restoreFile);
      staged = true;
      restoring = false;
      closeRestore();
      pushToast(t('component.databaseSettings.backupStaged'), 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      restoring = false;
    }
  }
</script>

<SettingsCard
  title={t('component.databaseSettings.title')}
  description={t('component.databaseSettings.description')}>
  <div class="flex flex-col gap-4">
    <Banner
      tone="warning"
      icon="warning"
      title={t('component.databaseSettings.credentialsTitle')}
      message={t('component.databaseSettings.credentialsMessage')} />

    {#if staged}
      <Banner
        tone="success"
        icon="check"
        title={t('component.databaseSettings.restoreReady')}
        message={t('component.databaseSettings.restoreReadyMessage')} />
    {/if}

    <div class="flex flex-wrap gap-2">
      <Button href={endpoints.systemBackup()} variant="secondary">
        <Icon name="download" size={14} />
        {t('component.databaseSettings.downloadBackup')}
      </Button>
      <Button variant="secondary" onclick={() => fileInput.click()}>
        <Icon name="upload" size={14} />
        {t('component.databaseSettings.restoreBackup')}
      </Button>
      <input
        class="sr-only"
        bind:this={fileInput}
        type="file"
        accept=".db,.sqlite,.sqlite3,application/vnd.sqlite3,application/x-sqlite3"
        aria-label={t('component.databaseSettings.chooseBackup')}
        onchange={chooseRestore} />
    </div>
  </div>
</SettingsCard>

{#if restoreFile}
  <Modal title={t('component.databaseSettings.restoreTitle')} width="max-w-md" dirty onclose={closeRestore}>
    <div class="flex flex-col gap-3 px-4 py-4">
      <p class="text-base text-ink-secondary">
        {t('component.databaseSettings.restorePrompt', { filename: restoreFile.name })}
      </p>
      <p class="text-base text-ink-secondary">
        {t('component.databaseSettings.restoreWarning')}
      </p>
    </div>
    {#snippet footer()}
      <Button variant="secondary" disabled={restoring} onclick={closeRestore}>{t('component.actions.cancel')}</Button>
      <Button variant="danger" disabled={restoring} onclick={restore}>
        {restoring ? t('component.databaseSettings.validating') : t('component.databaseSettings.stageRestore')}
      </Button>
    {/snippet}
  </Modal>
{/if}
