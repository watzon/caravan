<script lang="ts">
  /**
   * Dirty-eject recovery (SPEC §2.3, §13, PLAN phase 5 task 3).
   *
   * The server came up after an unclean shutdown — a pulled drive, a power cut.
   * Until it is verified, downloads refuse to resume, so this banner is the only
   * way back and it does not auto-dismiss.
   *
   * Caravan never runs fsck itself: repairing the filesystem it is running from
   * is the one recovery step that can make things worse, and the drive has to be
   * unmounted for it anyway. So the commands are printed for the user to run
   * from a terminal, and the button covers only what Caravan owns — sqlite's own
   * consistency check plus a library rescan.
   */
  import { api, errorText } from '../api/client';
  import { system } from '../state/system.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { useI18n } from '../i18n.svelte';
  import Banner from './Banner.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';

  interface Check {
    os: string;
    /** Run with the drive unmounted; the device/volume name is the user's. */
    command: string;
  }

  const CHECKS: Check[] = [
    { os: 'macOS', command: 'diskutil verifyVolume /Volumes/YOUR-DRIVE' },
    { os: 'Linux (exFAT)', command: 'sudo fsck.exfat /dev/sdX1' },
    { os: 'Linux (ext4)', command: 'sudo fsck.ext4 -f /dev/sdX1' },
    { os: 'Windows', command: 'chkdsk D: /f' },
  ];

  let busy = $state(false);
  const { t } = useI18n();

  async function verify() {
    busy = true;
    try {
      const result = await api.verifyIntegrity();
      // The server is the source of truth for the flag, so the banner goes away
      // because status says so — not because this component decided it had.
      await system.refresh();
      pushToast(
        result.scanning
          ? t('component.dirtyRecovery.verifiedScanning')
          : t('component.dirtyRecovery.verified'),
        'success',
      );
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busy = false;
    }
  }
</script>

<div class="flex flex-col gap-2">
  <Banner
    tone="danger"
    icon="warning"
    title={t('component.dirtyRecovery.title')}
    message={t('component.dirtyRecovery.message')}>
    {#snippet action()}
      <Button variant="primary" size="sm" disabled={busy} onclick={verify}>
        <Icon name="refresh" size={14} />
        {busy ? t('component.dirtyRecovery.verifying') : t('component.dirtyRecovery.verify')}
      </Button>
    {/snippet}
  </Banner>

  <details class="rounded-md border border-border bg-surface px-3 py-2">
    <summary class="cursor-pointer text-sm text-ink-secondary">
      {t('component.dirtyRecovery.filesystemCheck')}
    </summary>
    <p class="mt-2 text-sm text-ink-secondary">
      {t('component.dirtyRecovery.filesystemHelp')}
    </p>
    <ul class="mt-2 flex flex-col gap-1">
      {#each CHECKS as check (check.os)}
        <li class="flex flex-wrap items-baseline gap-2">
          <span class="w-32 shrink-0 text-sm text-ink-muted">{check.os}</span>
          <code class="font-mono text-xs text-ink">{check.command}</code>
        </li>
      {/each}
    </ul>
  </details>
</div>
