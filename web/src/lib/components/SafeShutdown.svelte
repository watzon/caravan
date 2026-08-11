<script lang="ts">
  /**
   * "Shut down safely" (SPEC §2.3, §11) — the portable drive's eject button.
   *
   * Portable mode only. A server install is stopped by whatever started it
   * (systemd, Docker), and a button that stops a machine you cannot reach the
   * UI of afterwards is a trap rather than a feature.
   *
   * The confirmation is not ceremony: this ends every download in progress and
   * logs everyone out of a server that cannot be restarted from here.
   */
  import { shutdown } from '../state/shutdown.svelte';
  import { useI18n } from '../i18n.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import LoadError from './LoadError.svelte';
  import Modal from './Modal.svelte';
  const { t } = useI18n();
</script>

<button
  type="button"
  class="flex items-center gap-2 rounded-md py-1 text-sm text-ink-secondary transition-colors duration-150 ease-out hover:text-ink disabled:opacity-50"
  disabled={shutdown.phase !== 'idle'}
  onclick={() => (shutdown.confirming = true)}>
  <Icon name="disk" size={14} />
  <span>{shutdown.phase === 'stopping' ? t('component.shutdown.stopping') : t('component.shutdown.safe')}</span>
</button>

{#if shutdown.error}
  <LoadError message={shutdown.error} />
{/if}

{#if shutdown.confirming}
  <Modal title={t('component.shutdown.confirmTitle')} width="max-w-md" onclose={() => (shutdown.confirming = false)}>
    <div class="flex flex-col gap-3 px-4 py-4">
      <p class="text-base text-ink-secondary">
        {t('component.shutdown.description')}
      </p>
      <p class="text-base text-ink-secondary">
        {t('component.shutdown.ejectWarning')}
      </p>
    </div>

    {#snippet footer()}
      <Button variant="secondary" onclick={() => (shutdown.confirming = false)}>{t('component.actions.cancel')}</Button>
      <Button variant="danger" onclick={() => shutdown.run()}>
        <Icon name="disk" size={14} />
        {t('component.shutdown.action')}
      </Button>
    {/snippet}
  </Modal>
{/if}
