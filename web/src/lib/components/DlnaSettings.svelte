<script lang="ts">
  /**
   * The built-in DLNA media server (SPEC §5.1, PLAN phase 4 task 2).
   *
   * There is nothing to configure beyond "on" and what the device calls itself:
   * a DLNA server either advertises the library on the LAN or it does not, and
   * everything else — what a client sees, how it plays — is fixed by the
   * protocol. So the two values are plain settings keys saved through
   * PUT /settings, and this component's real job is the status line: whether
   * SSDP actually came up, which the settings table cannot tell you.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import {
    SETTING_DLNA_ENABLED,
    SETTING_DLNA_FRIENDLY_NAME,
    type DlnaStatus,
    type Settings,
  } from '../api/types';
  import Badge from './Badge.svelte';
  import Banner from './Banner.svelte';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';
  import LoadError from './LoadError.svelte';
  import Skeleton from './Skeleton.svelte';
  import TextInput from './TextInput.svelte';
  import Toggle from './Toggle.svelte';

  interface Props {
    settings: Settings;
    saving?: boolean;
    onsave: (patch: Settings) => Promise<boolean>;
  }

  let { settings, saving = false, onsave }: Props = $props();

  let status = $state<DlnaStatus | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  let enabled = $state(true);
  let friendlyName = $state('');

  async function load() {
    loading = true;
    try {
      const loaded = await api.dlnaStatus();
      status = loaded;
      // The server owns the defaults — absent means on, and an empty name means
      // "Caravan" — so the form is seeded from what it reports, not from the
      // raw settings, which may have neither key.
      enabled = loaded.enabled;
      friendlyName = loaded.friendly_name;
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  async function save() {
    const ok = await onsave({
      [SETTING_DLNA_ENABLED]: String(enabled),
      [SETTING_DLNA_FRIENDLY_NAME]: friendlyName.trim(),
    });
    if (ok) await load();
  }

  // The server rejects a blank name rather than silently substituting the
  // default, so the button says so before the round trip.
  let canSave = $derived(!saving && friendlyName.trim() !== '');

  let unchanged = $derived(
    status !== null && status.enabled === enabled && status.friendly_name === friendlyName.trim(),
  );
</script>

<section class="flex flex-col gap-6">
  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && status === null}
    <div class="flex flex-col gap-4">
      <Skeleton class="h-4 w-32" />
      <Skeleton class="h-9 w-full" />
      <Skeleton class="h-8 w-24" />
    </div>
  {:else if status}
    <Toggle
      checked={enabled}
      label="Advertise this library on the local network"
      onchange={(next) => (enabled = next)} />

    <Field
      label="Device name"
      for="dlna-friendly-name"
      help="What the server calls itself in a TV's list of media servers.">
      <TextInput id="dlna-friendly-name" bind:value={friendlyName} placeholder="Caravan" />
    </Field>

    <Button variant="primary" class="self-start" disabled={!canSave} onclick={save}>
      <Icon name="check" size={14} />
      {saving ? 'Saving…' : 'Save'}
    </Button>

    <dl class="grid grid-cols-1 gap-4 rounded-md border border-border bg-surface p-4 sm:grid-cols-2">
      <div>
        <dt class="micro-label">Status</dt>
        <dd class="mt-1">
          {#if status.advertising}
            <Badge tone="success">Advertising</Badge>
          {:else if status.enabled}
            <Badge tone="warning">Not on the network</Badge>
          {:else}
            <Badge tone="neutral">Off</Badge>
          {/if}
        </dd>
      </div>
      <div class="min-w-0">
        <dt class="micro-label">Device ID</dt>
        <dd class="mt-1 truncate font-mono text-sm text-ink">{status.uuid || '—'}</dd>
      </div>
    </dl>

    {#if status.enabled && !status.advertising}
      <Banner
        tone="warning"
        icon="warning"
        title="Enabled, but not discoverable"
        message={status.error
          ? `Caravan could not join the discovery group: ${status.error}. Hosts without multicast - a container on a bridge network, or a VPN-only interface - cannot advertise.`
          : 'Caravan is not advertising yet. Restart the server to try again.'} />
    {:else if unchanged && status.advertising}
      <Banner
        tone="info"
        icon="check"
        message="Look for “{status.friendly_name}” under media servers, sources or inputs on your TV. Discovery can take a minute after a change." />
    {/if}
  {/if}
</section>
