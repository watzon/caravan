<script lang="ts">
  /** Settings for the embedded engine's durable defaults. */
  import type { Settings } from '../api/types';
  import {
    SETTING_ENGINE_LISTEN_PORT,
    SETTING_ENGINE_MAX_CONNECTIONS,
    SETTING_ENGINE_MAX_DOWN_KBPS,
    SETTING_ENGINE_MAX_UP_KBPS,
    SETTING_ENGINE_SEED_DAYS,
    SETTING_ENGINE_SEED_RATIO,
  } from '../api/types';
  import Banner from './Banner.svelte';
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

  let listenPort = $state('');
  let maxConnections = $state('');
  let maxDownKbps = $state('');
  let maxUpKbps = $state('');
  let seedRatio = $state('');
  let seedDays = $state('');
  let restartNotice = $state(false);
  // Deliberately a plain box rather than $state: this marks which settings
  // object the fields were last filled from, and tracking it would make the
  // effect below depend on something it writes on every run — which trips
  // Svelte's update-depth guard.
  const filledFrom: { settings: Settings | null } = { settings: null };

  $effect(() => {
    if (filledFrom.settings === settings) return;
    filledFrom.settings = settings;
    listenPort = settings[SETTING_ENGINE_LISTEN_PORT] ?? '0';
    maxConnections = settings[SETTING_ENGINE_MAX_CONNECTIONS] ?? '0';
    maxDownKbps = settings[SETTING_ENGINE_MAX_DOWN_KBPS] ?? '0';
    maxUpKbps = settings[SETTING_ENGINE_MAX_UP_KBPS] ?? '0';
    seedRatio = settings[SETTING_ENGINE_SEED_RATIO] ?? '0';
    seedDays = settings[SETTING_ENGINE_SEED_DAYS] ?? '0';
  });

  // A number input the user has cleared binds null, not '': String(null) is
  // the four characters "null", and the server rejects that as an invalid
  // setting. Every field here is "0 means off", so clearing one is a
  // legitimate way to say "no limit" and has to reach the server as 0.
  function valueOrZero(value: string | number | null | undefined): string {
    if (value === null || value === undefined) return '0';
    return String(value).trim() || '0';
  }

  async function save() {
    const port = valueOrZero(listenPort);
    const connections = valueOrZero(maxConnections);
    const changedAtRestart =
      port !== (settings[SETTING_ENGINE_LISTEN_PORT] ?? '0') ||
      connections !== (settings[SETTING_ENGINE_MAX_CONNECTIONS] ?? '0');
    const saved = await onsave({
      [SETTING_ENGINE_LISTEN_PORT]: port,
      [SETTING_ENGINE_MAX_CONNECTIONS]: connections,
      [SETTING_ENGINE_MAX_DOWN_KBPS]: valueOrZero(maxDownKbps),
      [SETTING_ENGINE_MAX_UP_KBPS]: valueOrZero(maxUpKbps),
      [SETTING_ENGINE_SEED_RATIO]: valueOrZero(seedRatio),
      [SETTING_ENGINE_SEED_DAYS]: valueOrZero(seedDays),
    });
    restartNotice = saved && changedAtRestart;
  }
</script>

<SettingsCard
  title="Torrent engine"
  description="Built in. Defaults for every torrent; a download can override its own limits from the queue.">
  {#snippet action()}
    <Button variant="primary" size="sm" disabled={saving} onclick={save}>
      <Icon name="check" size={14} />
      {saving ? 'Saving...' : 'Save changes'}
    </Button>
  {/snippet}

  <div class="flex flex-col gap-4">
    <h2 class="micro-label">Connection</h2>
    <Field label="Listen port" for="engine-listen-port" help="TCP and UDP. Forward this port for best swarm health.">
      <input
        id="engine-listen-port"
        type="number"
        min="0"
        max="65535"
        step="1"
        bind:value={listenPort}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>
    <Field label="Max connections" for="engine-max-connections" help="Per torrent. Applies to new downloads; changing the port restarts the engine.">
      <input
        id="engine-max-connections"
        type="number"
        min="0"
        step="1"
        bind:value={maxConnections}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>
  </div>

  <div class="flex flex-col gap-4">
    <h2 class="micro-label">Global rate limits</h2>
    <Field label="Download limit" for="engine-max-down-kbps" help="KB/s. 0 is unlimited.">
      <input
        id="engine-max-down-kbps"
        type="number"
        min="0"
        step="1"
        bind:value={maxDownKbps}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>
    <Field label="Upload limit" for="engine-max-up-kbps" help="KB/s. 0 is unlimited.">
      <input
        id="engine-max-up-kbps"
        type="number"
        min="0"
        step="1"
        bind:value={maxUpKbps}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>
  </div>

  <div class="flex flex-col gap-4">
    <h2 class="micro-label">Seeding targets</h2>
    <Field label="Stop seeding at ratio" for="engine-seed-ratio" help="0 disables this target.">
      <input
        id="engine-seed-ratio"
        type="number"
        min="0"
        step="0.01"
        bind:value={seedRatio}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>
    <Field label="Stop seeding after days" for="engine-seed-days" help="0 disables this target.">
      <input
        id="engine-seed-days"
        type="number"
        min="0"
        step="1"
        bind:value={seedDays}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>
    <Banner
      tone="info"
      icon="warning"
      title="Portable drive behavior"
      message="Portable drives pause seeding by default. A drive that can be unplugged keeps no open handles it cannot protect." />
  </div>

  {#if restartNotice}
    <p class="text-sm text-ink-muted">Port and connection changes apply after a restart.</p>
  {/if}
</SettingsCard>
