<script lang="ts">
  /**
   * The end of the safe-shutdown flow (SPEC §2.3). Rendered without the shell,
   * like first run and login: the server it would talk to is gone.
   *
   * Nothing here polls or retries. This screen's whole job is to be the last
   * thing on the page until the user unplugs the drive, and a reconnect attempt
   * that succeeded (someone restarted Caravan elsewhere) would only make it lie.
   */
  import Icon from '../components/Icon.svelte';
  import { shutdown } from '../state/shutdown.svelte';
  import { useI18n } from '../i18n.svelte';
  const { t } = useI18n();
</script>

<div class="flex min-h-full items-center justify-center bg-bg p-6">
  <div class="w-full max-w-sm rounded-lg border border-border bg-surface p-8 text-center">
    {#if shutdown.timedOut}
      <!-- The wait ran out before the process stopped answering, so this screen
           cannot make the promise it usually makes. Saying so is the only
           honest option: the alternative invites the user to pull a drive that
           may still be being written to. -->
      <span
        class="mx-auto flex size-10 items-center justify-center rounded-full bg-warning-tint text-warning"
        aria-hidden="true">
        <Icon name="warning" size={20} />
      </span>

      <h1 class="mt-4 font-display text-xl font-semibold tracking-tight text-ink">
        {t('route.safeToEject.stillStoppingTitle')}
      </h1>
      <p class="mt-2 text-base text-ink-secondary">
        {t('route.safeToEject.stillStoppingDescription')}
      </p>
    {:else}
      <span
        class="mx-auto flex size-10 items-center justify-center rounded-full bg-success-tint text-success"
        aria-hidden="true">
        <Icon name="check" size={20} />
      </span>

      <h1 class="mt-4 font-display text-xl font-semibold tracking-tight text-ink">
        {t('route.safeToEject.readyTitle')}
      </h1>
      <p class="mt-2 text-base text-ink-secondary">
        {t('route.safeToEject.readyDescription')}
      </p>
    {/if}
    <p class="mt-4 text-sm text-ink-muted">
      {t('route.safeToEject.reconnectDescription')}
    </p>
  </div>
</div>
