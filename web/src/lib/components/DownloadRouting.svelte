<script lang="ts">
  /**
   * Settings → Download clients → Routing (SPEC §5.1, PLAN phase 6 task 3).
   *
   * There is no per-grab engine choice anywhere in Caravan: a release is
   * routed on its protocol, so the only thing to configure is which engine
   * each protocol lands on. Torrents always have somewhere to go — the
   * built-in engine is the default and is offered even when external clients
   * exist. Usenet has no built-in engine, so leaving it unset is a real
   * configuration, and every usenet grab is then a recorded rejection rather
   * than a misroute.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import {
    ROUTE_EMBEDDED,
    SETTING_ROUTE_TORRENT,
    SETTING_ROUTE_USENET,
    type DownloadClient,
    type DownloadClientTypeInfo,
    type Protocol,
  } from '../api/types';
  import { describeType } from '../downloadClient';
  import { pushToast } from '../state/toast.svelte';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    /** The configured clients, so the pickers offer exactly what exists. */
    clients: DownloadClient[];
    types: DownloadClientTypeInfo[];
  }

  let { clients, types }: Props = $props();

  let torrent = $state(ROUTE_EMBEDDED);
  let usenet = $state('');
  let loading = $state(true);
  let saving = $state(false);
  let error = $state<string | null>(null);

  /**
   * The clients eligible for a protocol: enabled, and of a type that carries
   * it. A disabled client is not offered because its engine is not built —
   * picking it would leave the protocol unrouted without saying so.
   */
  function eligible(protocol: Protocol): DownloadClient[] {
    return clients.filter(
      (client) => client.enabled && describeType(types, client.type).protocol === protocol,
    );
  }

  let torrentClients = $derived(eligible('torrent'));
  let usenetClients = $derived(eligible('usenet'));

  /**
   * A stored id whose client is gone or disabled resolves to "nothing
   * configured" at grab time, so the picker shows that rather than a value
   * that is not really in effect.
   */
  function settle(value: string, options: DownloadClient[], fallback: string): string {
    if (value === fallback) return fallback;
    return options.some((client) => String(client.id) === value) ? value : fallback;
  }

  async function load() {
    loading = true;
    error = null;
    try {
      const settings = await api.getSettings();
      torrent = settings[SETTING_ROUTE_TORRENT] || ROUTE_EMBEDDED;
      usenet = settings[SETTING_ROUTE_USENET] || '';
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  async function save() {
    saving = true;
    try {
      await api.putSettings({
        [SETTING_ROUTE_TORRENT]: settle(torrent, torrentClients, ROUTE_EMBEDDED),
        [SETTING_ROUTE_USENET]: settle(usenet, usenetClients, ''),
      });
      pushToast('Routing saved.');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      saving = false;
    }
  }

  onMount(load);
</script>

<section
  class="flex flex-col gap-4 rounded-md border border-border bg-surface p-4"
  aria-label="Routing">
  <div>
    <h3 class="text-base font-medium text-ink">Routing</h3>
    <p class="mt-1 text-sm text-ink-secondary">
      Which engine takes a release is decided by its protocol, not per grab. Torznab results go to
      the torrent engine, Newznab results to the usenet one.
    </p>
  </div>

  {#if error}
    <p class="text-sm text-danger">{error}</p>
  {/if}

  <Field
    label="Torrent releases"
    for="route-torrent"
    help="Caravan's built-in engine needs no configuration and is always available.">
    <select
      id="route-torrent"
      bind:value={torrent}
      disabled={loading}
      class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink
             focus:border-accent focus:outline-none disabled:opacity-50">
      <option value={ROUTE_EMBEDDED}>Built-in engine</option>
      {#each torrentClients as client (client.id)}
        <option value={String(client.id)}>{client.name}</option>
      {/each}
    </select>
  </Field>

  <Field
    label="Usenet releases"
    for="route-usenet"
    help={usenetClients.length === 0
      ? 'No usenet client is configured, so usenet releases cannot be grabbed. Add a SABnzbd or NZBGet client above.'
      : 'There is no built-in usenet engine. Left unset, usenet grabs are rejected with that reason.'}>
    <select
      id="route-usenet"
      bind:value={usenet}
      disabled={loading}
      class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink
             focus:border-accent focus:outline-none disabled:opacity-50">
      <option value="">Not configured</option>
      {#each usenetClients as client (client.id)}
        <option value={String(client.id)}>{client.name}</option>
      {/each}
    </select>
  </Field>

  <Button variant="primary" class="self-start" disabled={loading || saving} onclick={save}>
    <Icon name="check" size={14} />
    {saving ? 'Saving…' : 'Save routing'}
  </Button>
</section>
