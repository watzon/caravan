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
  // Deliberately a plain box rather than $state: this marks which settings
  // object the fields were last filled from, and tracking it would make the
  // effect depend on something it writes on every run.
  const filledFrom: { settings: Settings | null } = { settings: null };

  $effect(() => {
    if (filledFrom.settings === settings) return;
    filledFrom.settings = settings;
    global = settings[SETTING_MAX_CONCURRENT_DOWNLOADS] ?? '0';
    torrent = settings[SETTING_EMBEDDED_TORRENT_MAX_CONCURRENT] ?? '0';
    usenet = settings[SETTING_EMBEDDED_USENET_MAX_CONCURRENT] ?? '0';
  });

  // A number input that the user has emptied binds null, not '': String(null)
  // is the four characters "null", and saving that would fail validation for a
  // field the user meant to clear. Blank is how you say "unlimited", so it has
  // to reach the server as 0.
  function valueOrZero(value: string | number | null | undefined): string {
    if (value === null || value === undefined) return '0';
    return String(value).trim() || '0';
  }

  async function save() {
    await onsave({
      [SETTING_MAX_CONCURRENT_DOWNLOADS]: valueOrZero(global),
      [SETTING_EMBEDDED_TORRENT_MAX_CONCURRENT]: valueOrZero(torrent),
      [SETTING_EMBEDDED_USENET_MAX_CONCURRENT]: valueOrZero(usenet),
    });
  }
</script>

<SettingsCard
  title="Concurrency"
  description="How many downloads run at once. 0 is unlimited — everything starts the moment it is grabbed, which is what makes ten downloads all crawl.">
  {#snippet action()}
    <Button variant="primary" size="sm" disabled={saving} onclick={save}>
      <Icon name="check" size={14} />
      {saving ? 'Saving...' : 'Save changes'}
    </Button>
  {/snippet}

  <div class="flex flex-col gap-4">
    <Field
      label="Max concurrent downloads"
      for="max-concurrent-downloads"
      help="Across every engine and download client together. Anything over the limit waits in the queue, oldest first.">
      <input
        id="max-concurrent-downloads"
        type="number"
        min="0"
        step="1"
        bind:value={global}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>

    <h2 class="micro-label">Per engine</h2>
    <Field
      label="Torrent engine"
      for="embedded-torrent-max-concurrent"
      help="The built-in torrent engine's own limit. The overall limit above still applies.">
      <input
        id="embedded-torrent-max-concurrent"
        type="number"
        min="0"
        step="1"
        bind:value={torrent}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>
    <Field
      label="Usenet engine"
      for="embedded-usenet-max-concurrent"
      help="A small number is right here — 2 is a good default. Parallel NZBs share one pool of connections to the same news servers, so a second download does not arrive faster; it halves both and doubles how long either takes to become importable.">
      <input
        id="embedded-usenet-max-concurrent"
        type="number"
        min="0"
        step="1"
        bind:value={usenet}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>
  </div>
</SettingsCard>
