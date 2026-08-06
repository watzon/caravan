<script lang="ts">
  /**
   * Settings → Downloads → Concurrency: how many downloads may transfer at once.
   *
   * This is the setting that turns the queue into a queue. Without it every
   * grab starts the moment it is added and they starve each other: ten
   * downloads all crawl, none finishes, and the first import is as far away as
   * the last. With it a few run at full speed and the rest wait their turn,
   * visibly, in the state the queue already has.
   */
  import type { Settings } from '../api/types';
  import {
    SETTING_EMBEDDED_TORRENT_MAX_CONCURRENT,
    SETTING_EMBEDDED_USENET_MAX_CONCURRENT,
    SETTING_MAX_CONCURRENT_DOWNLOADS,
  } from '../api/types';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';
  import SettingsCard from './SettingsCard.svelte';

  interface Props {
    settings: Settings;
    saving?: boolean;
    onsave: (patch: Settings) => Promise<boolean>;
  }

  let { settings, saving = false, onsave }: Props = $props();

  let global = $state('');
  let torrent = $state('');
  let usenet = $state('');
  let baseline = $state({
    global: '0',
    torrent: '0',
    usenet: '0',
  });
  // Deliberately a plain box rather than $state: this marks which settings
  // object the fields were last filled from, and tracking it would make the
  // effect depend on something it writes on every run.
  const filledFrom: { settings: Settings | null } = { settings: null };

  function normalizedInteger(value: string | number | null | undefined): string | null {
    const text = value === null || value === undefined ? '' : String(value).trim();
    if (text === '') return '0';
    if (!/^\d+$/.test(text)) return null;

    const number = Number(text);
    if (!Number.isSafeInteger(number) || number < 0) return null;
    return String(number);
  }

  $effect(() => {
    if (filledFrom.settings === settings) return;
    filledFrom.settings = settings;
    const loaded = {
      global: normalizedInteger(settings[SETTING_MAX_CONCURRENT_DOWNLOADS]) ?? '0',
      torrent: normalizedInteger(settings[SETTING_EMBEDDED_TORRENT_MAX_CONCURRENT]) ?? '0',
      usenet: normalizedInteger(settings[SETTING_EMBEDDED_USENET_MAX_CONCURRENT]) ?? '0',
    };
    baseline = loaded;
    global = loaded.global;
    torrent = loaded.torrent;
    usenet = loaded.usenet;
  });

  let normalizedGlobal = $derived(normalizedInteger(global));
  let normalizedTorrent = $derived(normalizedInteger(torrent));
  let normalizedUsenet = $derived(normalizedInteger(usenet));
  let canSave = $derived(
    normalizedGlobal !== null && normalizedTorrent !== null && normalizedUsenet !== null,
  );
  let hasChanges = $derived(
    canSave &&
      (normalizedGlobal !== baseline.global ||
        normalizedTorrent !== baseline.torrent ||
        normalizedUsenet !== baseline.usenet),
  );

  async function save() {
    if (
      !canSave ||
      !hasChanges ||
      normalizedGlobal === null ||
      normalizedTorrent === null ||
      normalizedUsenet === null
    ) {
      return;
    }

    const patch = {
      [SETTING_MAX_CONCURRENT_DOWNLOADS]: normalizedGlobal,
      [SETTING_EMBEDDED_TORRENT_MAX_CONCURRENT]: normalizedTorrent,
      [SETTING_EMBEDDED_USENET_MAX_CONCURRENT]: normalizedUsenet,
    };
    if (await onsave(patch)) {
      baseline = { global: normalizedGlobal, torrent: normalizedTorrent, usenet: normalizedUsenet };
    }
  }
</script>

<SettingsCard
  title="Concurrency"
  description="How many downloads run at once. 0 is unlimited - everything starts the moment it is grabbed, which is what makes ten downloads all crawl.">
  {#snippet action()}
    <Button variant="primary" size="sm" disabled={saving || !canSave || !hasChanges} onclick={save}>
      <Icon name="check" size={14} />
      {saving ? 'Saving...' : !canSave ? 'Fix errors' : !hasChanges ? 'No changes' : 'Save changes'}
    </Button>
  {/snippet}

  <div class="flex flex-col gap-4">
    <Field
      label="Max concurrent downloads"
      for="max-concurrent-downloads"
      help="Across every engine and download client together. Anything over the limit waits in the queue, oldest first."
      error={normalizedGlobal === null ? 'Enter a non-negative whole number.' : null}>
      <input
        id="max-concurrent-downloads"
        type="number"
        min="0"
        step="1"
        aria-invalid={normalizedGlobal === null}
        bind:value={global}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>
  </div>

  <div data-settings-advanced class="flex flex-col gap-4">
    <h2 class="micro-label">Per engine</h2>
    <Field
      label="Torrent engine"
      for="embedded-torrent-max-concurrent"
      help="The built-in torrent engine's own limit. The overall limit above still applies."
      error={normalizedTorrent === null ? 'Enter a non-negative whole number.' : null}>
      <input
        id="embedded-torrent-max-concurrent"
        type="number"
        min="0"
        step="1"
        aria-invalid={normalizedTorrent === null}
        bind:value={torrent}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>
    <Field
      label="Usenet engine"
      for="embedded-usenet-max-concurrent"
      help="A small number is right here - 2 is a good default. Parallel NZBs share one pool of connections to the same news servers, so a second download does not arrive faster; it halves both and doubles how long either takes to become importable."
      error={normalizedUsenet === null ? 'Enter a non-negative whole number.' : null}>
      <input
        id="embedded-usenet-max-concurrent"
        type="number"
        min="0"
        step="1"
        aria-invalid={normalizedUsenet === null}
        bind:value={usenet}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>
  </div>
</SettingsCard>
