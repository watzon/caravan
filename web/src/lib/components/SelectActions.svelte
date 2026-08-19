<script lang="ts">
  import { useI18n } from '../i18n.svelte';
  /**
   * The floating action bar for a grid selection, and the one confirm the bulk
   * removal goes through. It exists only while something is selected — the
   * selection itself starts on the cards — and sits pinned near the bottom of
   * the view, out of the toolbar's way.
   *
   * Movies and Series share it because the only difference between them is
   * which per-item endpoints the actions call — passed in as `actions` — and
   * the noun in the confirm.
   *
   * Search queues wanted items only. Monitor and search turns monitoring on
   * first so the same wanted list then includes the selection.
   */
  import { bulkSummary, runBulk } from '../bulk';
  import type { Selection } from '../selection.svelte';
  import { libraryChanged } from '../state/activity';
  import { pushToast } from '../state/toast.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import RemoveItemModal from './RemoveItemModal.svelte';

  interface Actions {
    search: (id: number) => Promise<unknown>;
    setMonitored: (id: number, monitored: boolean) => Promise<unknown>;
    remove: (id: number, deleteFiles: boolean) => Promise<unknown>;
  }

  interface Props {
    selection: Selection;
    /** Nouns for the confirm; "series" is its own plural. */
    noun: string;
    plural: string;
    actions: Actions;
    /** Reload the list after an action changed it. */
    onchanged: () => void | Promise<void>;
  }

  let { selection, noun, plural, actions, onchanged }: Props = $props();

  let subject = $derived(t('component.selectActions.selectedNoun', { count: selection.count, noun: selection.count === 1 ? noun : plural }));

  let busy = $state(false);
  let confirmingRemove = $state(false);

  async function run(verb: string, action: (id: number) => Promise<unknown>) {
    const ids = [...selection.ids];
    if (ids.length === 0) return;
    busy = true;
    // runBulk counts failures instead of throwing, so there is one exit and one
    // toast whether every item worked or none did.
    const result = await runBulk(ids, action);
    busy = false;
    if (result.ok > 0) libraryChanged();
    pushToast(bulkSummary(result, verb), result.failed === 0 ? 'success' : 'danger');
  }

  // Removal reloads the list unconditionally: a partial failure still changed
  // it, and the grid must not keep showing items that are gone.
  async function remove(deleteFiles: boolean) {
    const ids = [...selection.ids];
    busy = true;
    const result = await runBulk(ids, (id) => actions.remove(id, deleteFiles));
    busy = false;
    confirmingRemove = false;
    selection.clear();
    pushToast(bulkSummary(result, t('component.selectActions.removed')), result.failed === 0 ? 'success' : 'danger');
    if (result.ok > 0) libraryChanged();
    await onchanged();
  }

  /** Escape drops the selection, the same way it closes a dialog. */
  function onkeydown(event: KeyboardEvent) {
    if (event.key === 'Escape' && selection.active && !confirmingRemove) {
      selection.clear();
    }
  }

  const { t, tp } = useI18n();
</script>

<svelte:window {onkeydown} />

{#if selection.active}
  <!-- Centered over the content column at desktop widths, and kept inside the
       viewport while the sidebar is off-canvas on phones. -->
  <div class="pointer-events-none fixed bottom-3 left-0 right-0 z-40 flex justify-center px-3 md:bottom-6 md:left-60">
    <div
      class="pointer-events-auto flex max-w-full flex-wrap items-center justify-center gap-1 rounded-lg
             border border-border-strong bg-overlay px-1.5 py-1.5 shadow-2xl"
      role="group"
      aria-label={t('component.selectActions.actionsFor', { subject })}>
      <span
        aria-live="polite"
        class="mr-2 basis-full whitespace-nowrap px-2 text-center text-base font-medium text-ink sm:basis-auto sm:px-0">
        {t('component.selectActions.selected', { count: selection.count })}
      </span>

      <Button
        variant="ghost"
        size="sm"
        disabled={busy}
        onclick={() => run(t('component.selectActions.queuedSearches'), actions.search)}>
        <Icon name="search" size={14} />
        {t('component.selectActions.search')}
      </Button>
      <!-- Search only queues wanted (already-monitored) items. This monitors
           first so a mixed or unmonitored selection is actually searchable. -->
      <Button
        variant="ghost"
        size="sm"
        disabled={busy}
        onclick={() =>
          run(t('component.selectActions.monitoredAndSearched'), async (id) => {
            await actions.setMonitored(id, true);
            await actions.search(id);
          })}>
        {t('component.selectActions.monitorAndSearch')}
      </Button>
      <Button
        variant="ghost"
        size="sm"
        disabled={busy}
        onclick={() => run(t('component.selectActions.monitored'), (id) => actions.setMonitored(id, true))}>
        {t('component.selectActions.monitor')}
      </Button>
      <Button
        variant="ghost"
        size="sm"
        disabled={busy}
        onclick={() => run(t('component.selectActions.unmonitored'), (id) => actions.setMonitored(id, false))}>
        {t('component.selectActions.unmonitor')}
      </Button>
      <Button
        variant="danger"
        size="sm"
        disabled={busy}
        onclick={() => (confirmingRemove = true)}>
        <Icon name="trash" size={14} />
        {t('component.selectActions.remove')}
      </Button>

      <span class="mx-1 hidden h-5 w-px bg-border sm:block" aria-hidden="true"></span>

      <Button
        variant="ghost"
        size="sm"
        disabled={busy}
        onclick={() => selection.clear()}
        title={t('component.selectActions.clearSelection')}>
        <Icon name="close" size={14} />
        <span class="sr-only">{t('component.selectActions.clearSelection')}</span>
      </Button>
    </div>
  </div>
{/if}

{#if confirmingRemove}
  <RemoveItemModal
    title={t('component.selectActions.removeTitle', { subject: selection.count === 1 ? noun : plural })}
    {subject}
    {busy}
    onconfirm={remove}
    onclose={() => (confirmingRemove = false)} />
{/if}
