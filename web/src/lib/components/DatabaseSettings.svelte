<script lang="ts">
  import { api, endpoints, errorText } from '../api/client';
  import { pushToast } from '../state/toast.svelte';
  import Banner from './Banner.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import Modal from './Modal.svelte';
  import SettingsCard from './SettingsCard.svelte';

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
      pushToast('Backup staged. Restart Caravan to apply it.', 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      restoring = false;
    }
  }
</script>

<SettingsCard
  title="Database backup"
  description="Download a consistent copy of Caravan’s database, or stage one to replace it after a restart.">
  <div class="flex flex-col gap-4">
    <Banner
      tone="warning"
      icon="warning"
      title="Backups contain credentials"
      message="A backup includes API keys, download-client passwords, user accounts, and library history. Store it as securely as the Caravan server." />

    {#if staged}
      <Banner
        tone="success"
        icon="check"
        title="Restore ready"
        message="The current database stays active until Caravan restarts. The database it replaces is kept as a before-restore recovery copy." />
    {/if}

    <div class="flex flex-wrap gap-2">
      <Button href={endpoints.systemBackup()} variant="secondary">
        <Icon name="download" size={14} />
        Download backup
      </Button>
      <Button variant="secondary" onclick={() => fileInput.click()}>
        <Icon name="upload" size={14} />
        Restore backup
      </Button>
      <input
        class="sr-only"
        bind:this={fileInput}
        type="file"
        accept=".db,.sqlite,.sqlite3,application/vnd.sqlite3,application/x-sqlite3"
        aria-label="Choose Caravan backup"
        onchange={chooseRestore} />
    </div>
  </div>
</SettingsCard>

{#if restoreFile}
  <Modal title="Restore this database?" width="max-w-md" dirty onclose={closeRestore}>
    <div class="flex flex-col gap-3 px-4 py-4">
      <p class="text-base text-ink-secondary">
        Caravan will validate <span class="font-mono break-all text-ink">{restoreFile.name}</span> now and apply it after the next restart.
      </p>
      <p class="text-base text-ink-secondary">
        The restored database replaces settings, users, profiles, and library history. Media files are not changed. The current database is kept as a recovery copy.
      </p>
    </div>
    {#snippet footer()}
      <Button variant="secondary" disabled={restoring} onclick={closeRestore}>Cancel</Button>
      <Button variant="danger" disabled={restoring} onclick={restore}>
        {restoring ? 'Validating…' : 'Stage restore'}
      </Button>
    {/snippet}
  </Modal>
{/if}
