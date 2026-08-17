<script lang="ts">
  /**
   * "Move to library" confirmation for a detail page's overflow menu.
   *
   * The move itself is a durable background job — a series can be hundreds of
   * files — so confirming answers immediately and the caller shows a toast;
   * the activity feed carries the completion. The dropdown offers every
   * library that ACCEPTS the item's kind except the one it is already in:
   * moving to where it lives is a no-op nobody needs a button for.
   *
   * Accepts rather than matches, because an anime library holds films and
   * series together — so a film may move onto an anime shelf and a series may
   * move between the television and anime shelves. The server rewrites
   * `series.kind` to match the destination, so the row and the shelf never
   * disagree afterwards.
   */
  import { api, errorText } from '../api/client';
  import type { LibraryKind } from '../api/types';
  import { libraries } from '../state/libraries.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { useI18n } from '../i18n.svelte';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import Modal from './Modal.svelte';

  interface Props {
    /** 'movie' moves through the movie endpoint, everything else the series one. */
    itemType: 'movie' | 'series';
    itemID: number;
    itemTitle: string;
    /** The item's OWN vocabulary — 'movie', 'tv', 'adult' — not its shelf's. */
    kind: LibraryKind;
    /** The library the item is in now, excluded from the choices. */
    currentLibraryID: number;
    onclose: () => void;
    /** Called after the move is queued, with the target's name. */
    onmoved?: (libraryName: string) => void;
  }

  let { itemType, itemID, itemTitle, kind, currentLibraryID, onclose, onmoved }: Props = $props();
  const { t } = useI18n();

  let busy = $state(false);
  let choices = $derived(libraries.accepting(kind).filter((l) => l.id !== currentLibraryID));
  let targetID = $state(0);
  $effect(() => {
    if (targetID === 0 && choices.length > 0) targetID = choices[0]!.id;
  });

  async function move() {
    const target = choices.find((l) => l.id === targetID);
    if (!target) return;
    busy = true;
    try {
      if (itemType === 'movie') {
        await api.moveMovie(itemID, target.id);
      } else {
        await api.moveSeries(itemID, target.id);
      }
      pushToast(t('component.moveItem.queued', { title: itemTitle, library: target.name }), 'success');
      onmoved?.(target.name);
      onclose();
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busy = false;
    }
  }
</script>

<Modal title={t('component.moveItem.title', { title: itemTitle })} width="max-w-md" {onclose}>
  <div class="flex flex-col gap-4 p-4">
    <Field label={t('component.moveItem.target')} for="move-target" help={t('component.moveItem.help')}>
      <select
        id="move-target"
        bind:value={targetID}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink focus:border-accent focus:outline-none">
        {#each choices as choice (choice.id)}
          <option value={choice.id}>{choice.name}</option>
        {/each}
      </select>
    </Field>
  </div>
  {#snippet footer()}
    <div class="flex w-full flex-wrap items-center justify-end gap-2">
      <Button variant="ghost" disabled={busy} onclick={onclose}>{t('component.actions.cancel')}</Button>
      <Button variant="primary" disabled={busy || choices.length === 0} onclick={move}>
        {t('component.actions.move')}
      </Button>
    </div>
  {/snippet}
</Modal>
