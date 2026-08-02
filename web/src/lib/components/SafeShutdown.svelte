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
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import LoadError from './LoadError.svelte';
  import Modal from './Modal.svelte';
</script>

<button
  type="button"
  class="flex items-center gap-2 rounded-md py-1 text-sm text-ink-secondary transition-colors duration-150 ease-out hover:text-ink disabled:opacity-50"
  disabled={shutdown.phase !== 'idle'}
  onclick={() => (shutdown.confirming = true)}>
  <Icon name="disk" size={14} />
  <span>{shutdown.phase === 'stopping' ? 'Shutting down…' : 'Shut down safely'}</span>
</button>

{#if shutdown.error}
  <LoadError message={shutdown.error} />
{/if}

{#if shutdown.confirming}
  <Modal title="Shut down Caravan?" width="max-w-md" onclose={() => (shutdown.confirming = false)}>
    <div class="flex flex-col gap-3 px-4 py-4">
      <p class="text-base text-ink-secondary">
        Caravan will stop its downloads, flush the database and release the drive.
        Once it has, this page can no longer reach it.
      </p>
      <p class="text-base text-ink-secondary">
        Wait for the "safe to eject" screen before unplugging the drive.
      </p>
    </div>

    {#snippet footer()}
      <Button variant="secondary" onclick={() => (shutdown.confirming = false)}>Cancel</Button>
      <Button variant="danger" onclick={() => shutdown.run()}>
        <Icon name="disk" size={14} />
        Shut down
      </Button>
    {/snippet}
  </Modal>
{/if}
