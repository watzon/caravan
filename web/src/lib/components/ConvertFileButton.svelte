<script lang="ts">
  /**
   * The per-file convert affordance (SPEC §8): the one place in the library
   * where a compatibility verdict becomes an action.
   *
   * It renders nothing at all when there is nothing to offer: a file the
   * profile is happy with, a file nothing could be judged about, or a server
   * without ffmpeg. SPEC §8 is explicit that a missing ffmpeg hides
   * Convert-for-TV and leaves the warning informational, and a disabled button
   * with a tooltip is not hidden.
   */
  import { api, errorText } from '../api/client';
  import { ApiError } from '../api/client';
  import type { MediaFile } from '../api/types';
  import { convertible } from '../conversion';
  import { system } from '../state/system.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { useI18n } from '../i18n.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    file: MediaFile;
    /** Icon-only, for the episode table where a full label would not fit. */
    compact?: boolean;
    /** Called after this file enters an open conversion, including a 409 race. */
    onqueued?: () => void;
  }

  let { file, compact = false, onqueued }: Props = $props();
  const { t } = useI18n();

  let busy = $state(false);
  let queued = $state(false);

  let shown = $derived((system.status?.ffmpeg_available ?? false) && convertible(file.compatibility));

  const label = t('component.convertFile.label');

  async function convert() {
    busy = true;
    let notifyQueued = false;
    try {
      await api.convertMediaFile(file.id);
      queued = true;
      notifyQueued = true;
      pushToast(t('component.convertFile.queued'), 'neutral');
    } catch (err) {
      // Already queued is not a failure the user caused twice; say so and
      // leave the button in its queued state.
      if (err instanceof ApiError && err.status === 409) {
        queued = true;
        notifyQueued = true;
        pushToast(errorText(err), 'neutral');
      } else {
        pushToast(errorText(err), 'danger');
      }
    } finally {
      busy = false;
    }
    if (notifyQueued) onqueued?.();
  }
</script>

{#if shown}
  {#if queued}
    <Button variant="ghost" size="sm" href="/convert" title={t('component.convertFile.openQueue')}>
      <Icon name="refresh" size={14} />
      {#if compact}
        <span class="sr-only">{t('component.convertFile.inQueue')}</span>
      {:else}
        {t('component.convertFile.inQueue')}
      {/if}
    </Button>
  {:else}
    <Button
      variant={compact ? 'ghost' : 'secondary'}
      size="sm"
      disabled={busy}
      title={file.compatibility?.reasons?.length
        ? `${label}: ${file.compatibility.reasons.join('; ')}`
        : label}
      onclick={convert}>
      <Icon name="refresh" size={14} />
      {#if compact}
        <span class="sr-only">{label}</span>
      {:else}
        {label}
      {/if}
    </Button>
  {/if}
{/if}
