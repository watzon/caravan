<script lang="ts">
  /**
   * First run (SPEC §10.1): pick the storage root, optionally queue a library
   * scan. There is no further wizard — everything else ships with defaults.
   */
  import { api, errorText } from '../api/client';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import TextInput from '../components/TextInput.svelte';
  import Toggle from '../components/Toggle.svelte';
  import { navigate } from '../router.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { system } from '../state/system.svelte';

  let root = $state(system.status?.storage_root ?? '');
  let scanNow = $state(true);
  let saving = $state(false);
  let error = $state<string | null>(null);

  async function save() {
    const value = root.trim();
    if (value === '') {
      error = 'Enter the folder Caravan should treat as its storage root.';
      return;
    }

    saving = true;
    error = null;
    try {
      // The repoint endpoint rather than a plain settings write: the storage
      // root is the one setting with rules attached — absolute, existing, a
      // folder — and typing a path that does not exist is exactly the mistake
      // a first run makes. PUT /settings enforces none of them.
      await api.repointStorageRoot(value);
      await system.refresh();

      if (scanNow) {
        // The scan is the slow part; failing it must not strand the user on
        // the first-run screen with a saved root.
        try {
          await api.rescan();
          const summary = await api.awaitScan();
          pushToast(
            `Scan finished: ${summary.media_files} files in the library, ${summary.unmatched} unmatched.`,
            summary.unmatched > 0 ? 'warning' : 'success',
          );
        } catch (err) {
          pushToast(`Storage root saved, but the scan failed: ${errorText(err)}`, 'warning');
        }
      }

      navigate(scanNow ? '/scan-review' : '/movies');
    } catch (err) {
      error = errorText(err);
    } finally {
      saving = false;
    }
  }
</script>

<div class="flex min-h-full items-center justify-center bg-bg p-6">
  <div class="w-full max-w-lg rounded-lg border border-border bg-surface p-8">
    <div class="mb-6 flex items-center gap-3">
      <span
        class="flex size-8 items-center justify-center rounded-md bg-accent text-ink-inverse"
        aria-hidden="true">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M3 17h18" />
          <path d="M5 17V9l4-4h7a3 3 0 0 1 3 3v9" />
          <circle cx="8" cy="19" r="2" />
          <circle cx="17" cy="19" r="2" />
        </svg>
      </span>
      <span class="font-display text-lg font-bold tracking-tight text-ink">CARAVAN</span>
    </div>

    <h1 class="font-display text-xl font-semibold tracking-tight text-ink">
      Choose a storage root
    </h1>
    <p class="mt-2 text-base text-ink-secondary">
      Everything Caravan stores lives under one folder, and every path in the database is
      relative to it. Point it at your media now; you can re-point it later without moving a
      file.
    </p>

    <form
      class="mt-6 flex flex-col gap-6"
      onsubmit={(event) => {
        event.preventDefault();
        save();
      }}>
      <Field
        label="Storage root"
        for="storage-root"
        help="Absolute path on the machine running Caravan — /data in Docker, the drive root on a portable disk.">
        <TextInput
          id="storage-root"
          bind:value={root}
          mono
          autofocus
          placeholder="/data" />
      </Field>

      <Toggle
        checked={scanNow}
        label="Scan for existing media now"
        onchange={(next) => (scanNow = next)} />

      {#if error}
        <LoadError message={error} />
      {/if}

      <Button variant="primary" type="submit" disabled={saving} class="self-start">
        <Icon name="check" size={14} />
        {saving ? 'Saving…' : 'Save and continue'}
      </Button>
    </form>
  </div>
</div>
