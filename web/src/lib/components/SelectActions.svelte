<script lang="ts">
  /**
   * Select mode's controls for the library grids: the button that enters it,
   * the action bar that appears while it is on, and the one confirm the bulk
   * removal goes through.
   *
   * Movies and Series share it because the only difference between them is
   * which per-item endpoints the actions call — passed in as `actions` — and
   * the noun in the confirm.
   */
  import { bulkSummary, runBulk } from '../bulk';
  import type { Selection } from '../selection.svelte';
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

  let subject = $derived(`${selection.count} selected ${selection.count === 1 ? noun : plural}`);

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
    selection.exit();
    pushToast(bulkSummary(result, 'Removed'), result.failed === 0 ? 'success' : 'danger');
    await onchanged();
  }

  /** Escape leaves select mode, the same way it closes a dialog. */
  function onkeydown(event: KeyboardEvent) {
    if (event.key === 'Escape' && selection.active && !confirmingRemove) {
      selection.exit();
    }
  }
</script>

<svelte:window {onkeydown} />

{#if !selection.active}
  <Button variant="secondary" onclick={() => selection.enter()}>
    <Icon name="check" size={14} />
    Select
  </Button>
{/if}

{#if selection.active}
  <!-- Full width so it wraps onto its own row directly under the chips. -->
  <div
    class="flex w-full flex-wrap items-center gap-2 rounded-md border border-border-strong
           bg-surface px-3 py-2"
    role="group"
    aria-label="Selection actions">
    <span class="text-base text-ink">{selection.count} selected</span>

    <div class="ml-auto flex flex-wrap items-center gap-2">
      <Button
        variant="secondary"
        size="sm"
        disabled={busy || selection.count === 0}
        onclick={() => run('Queued searches for', actions.search)}>
        <Icon name="search" size={14} />
        Search
      </Button>
      <Button
        variant="secondary"
        size="sm"
        disabled={busy || selection.count === 0}
        onclick={() => run('Monitored', (id) => actions.setMonitored(id, true))}>
        Monitor
      </Button>
      <Button
        variant="secondary"
        size="sm"
        disabled={busy || selection.count === 0}
        onclick={() => run('Unmonitored', (id) => actions.setMonitored(id, false))}>
        Unmonitor
      </Button>
      <Button
        variant="danger"
        size="sm"
        disabled={busy || selection.count === 0}
        onclick={() => (confirmingRemove = true)}>
        <Icon name="trash" size={14} />
        Remove…
      </Button>
      <!-- Named "Done", not "Cancel": the confirm dialog owns that word, and
           leaving select mode undoes nothing that was already applied. -->
      <Button variant="ghost" size="sm" disabled={busy} onclick={() => selection.exit()}>
        Done
      </Button>
    </div>
  </div>
{/if}

{#if confirmingRemove}
  <RemoveItemModal
    title="Remove {selection.count === 1 ? noun : plural}"
    {subject}
    {busy}
    onconfirm={remove}
    onclose={() => (confirmingRemove = false)} />
{/if}
