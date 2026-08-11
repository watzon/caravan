<script lang="ts">
  /**
   * Confirm for removing library items. The checkbox is the whole decision:
   * untracking leaves the media alone and a rescan re-adds it, deleting the
   * files does not. It defaults to off and is not offered when there is
   * nothing on disk to delete.
   */
  import Button from './Button.svelte';
  import Modal from './Modal.svelte';
  import { useI18n } from '../i18n.svelte';

  interface Props {
    title: string;
    /** What is being removed, e.g. "Dune (2021)" or "5 movies". */
    subject: string;
    /** Files "also delete" would remove, or null when the count is unknown. */
    fileCount?: number | null;
    busy?: boolean;
    onconfirm: (deleteFiles: boolean) => void;
    onclose: () => void;
  }

  let { title, subject, fileCount = null, busy = false, onconfirm, onclose }: Props = $props();

  let deleteFiles = $state(false);

  let offerFiles = $derived(fileCount === null || fileCount > 0);
  const { t, tp } = useI18n();
  let filesLabel = $derived(
    fileCount === null
      ? t('component.removeItem.deleteFiles')
      : tp('component.removeItem.deleteFilesCount', fileCount),
  );
</script>

<Modal {title} width="max-w-lg" {onclose}>
  <div class="flex flex-col gap-3 p-4">
    <p class="text-base text-ink">{subject}</p>
    <p class="text-base text-ink-secondary">
      {t('component.removeItem.description')}
    </p>

    {#if offerFiles}
      <label class="flex items-start gap-2 text-base text-ink">
        <input
          type="checkbox"
          class="mt-1 accent-danger"
          checked={deleteFiles}
          onchange={(event) => (deleteFiles = event.currentTarget.checked)} />
        <span>
          {filesLabel}
          <span class="block text-sm text-ink-secondary">
            {t('component.removeItem.irreversible')}
          </span>
        </span>
      </label>
    {/if}
  </div>

  {#snippet footer()}
    <Button variant="ghost" disabled={busy} onclick={onclose}>{t('component.actions.cancel')}</Button>
    <Button variant="danger" disabled={busy} onclick={() => onconfirm(deleteFiles)}>
      {busy ? t('component.actions.removing') : t('component.actions.remove')}
    </Button>
  {/snippet}
</Modal>
