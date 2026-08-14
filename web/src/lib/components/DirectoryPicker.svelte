<script lang="ts">
  /**
   * Host-folder picker. The text field stays the source of truth so a path can
   * still be typed or pasted; Browse lists folders on the machine running
   * Caravan, which is the disk the storage root has to exist on.
   */
  import { api, errorText } from '../api/client';
  import type { DirectoryListing } from '../api/types';
  import { useI18n } from '../i18n.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import Modal from './Modal.svelte';
  import TextInput from './TextInput.svelte';

  interface Props {
    id?: string;
    value: string;
    placeholder?: string;
    autofocus?: boolean;
    disabled?: boolean;
    class?: string;
  }

  let {
    id,
    value = $bindable(''),
    placeholder,
    autofocus = false,
    disabled = false,
    class: klass = '',
  }: Props = $props();

  const { t } = useI18n();

  let open = $state(false);
  let listing = $state<DirectoryListing | null>(null);
  let loading = $state(false);
  let error = $state<string | null>(null);

  async function load(path: string) {
    loading = true;
    error = null;
    try {
      listing = await api.listDirectories(path);
    } catch (err) {
      if (path !== '') {
        try {
          listing = await api.listDirectories('');
          error = null;
          return;
        } catch {
          // Fall through to the original error.
        }
      }
      listing = null;
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  function browse() {
    open = true;
    void load(value.trim());
  }

  function close() {
    open = false;
    listing = null;
    error = null;
  }

  function useFolder() {
    if (!listing) return;
    value = listing.path;
    close();
  }
</script>

<div class="flex items-stretch gap-2 {klass}">
  <TextInput
    {id}
    bind:value
    {placeholder}
    {autofocus}
    {disabled}
    mono
    class="min-w-0 flex-1" />
  <Button variant="secondary" {disabled} onclick={browse} class="shrink-0">
    <Icon name="folder" size={14} />
    {t('component.directoryPicker.browse')}
  </Button>
</div>

{#if open}
  <Modal title={t('component.directoryPicker.title')} width="max-w-lg" onclose={close}>
    <div class="flex flex-col gap-3 px-4 py-4">
      <div class="flex items-center gap-2">
        {#if listing && (listing.parent || (listing.path !== '' && listing.path !== '/'))}
          <Button variant="secondary" size="sm" onclick={() => void load(listing!.parent)}>
            <Icon name="back" size={14} />
            {t('component.directoryPicker.up')}
          </Button>
        {/if}
        <p class="min-w-0 flex-1 truncate font-mono text-sm text-ink-secondary" title={listing?.path}>
          {listing?.path || '—'}
        </p>
      </div>

      {#if loading}
        <p class="text-sm text-ink-muted">{t('component.directoryPicker.loading')}</p>
      {:else if error}
        <p class="text-sm text-danger">{error}</p>
      {:else if listing && listing.directories.length === 0}
        <p class="text-sm text-ink-muted">{t('component.directoryPicker.empty')}</p>
      {:else if listing}
        <ul class="flex max-h-72 flex-col overflow-y-auto rounded-md border border-border">
          {#each listing.directories as dir (dir.path)}
            <li>
              <button
                type="button"
                class="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-ink hover:bg-raised"
                onclick={() => void load(dir.path)}>
                <Icon name="folder" size={14} class="shrink-0 text-ink-muted" />
                <span class="min-w-0 truncate">{dir.name}</span>
              </button>
            </li>
          {/each}
        </ul>
      {/if}
    </div>
    {#snippet footer()}
      <Button variant="secondary" onclick={close}>{t('component.actions.cancel')}</Button>
      <Button variant="primary" disabled={!listing || listing.path === ''} onclick={useFolder}>
        <Icon name="check" size={14} />
        {t('component.directoryPicker.useFolder')}
      </Button>
    {/snippet}
  </Modal>
{/if}
