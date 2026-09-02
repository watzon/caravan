<script lang="ts">
  /**
   * Settings → Download clients → Routing (SPEC §5.1).
   *
   * There is no per-grab engine choice anywhere in Caravan: a release is routed
   * on its protocol, so the only thing to configure is which engine each
   * protocol lands on. Both protocols always have somewhere to go: each has a
   * built-in engine that is the default and is offered even when external
   * clients exist, so an external client is a choice rather than a requirement.
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
  import { useI18n } from '../i18n.svelte';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';
  import LocalizedText from './LocalizedText.svelte';

  interface Props {
    /** The configured clients, so the pickers offer exactly what exists. */
    clients: DownloadClient[];
    types: DownloadClientTypeInfo[];
  }

  let { clients, types }: Props = $props();
  const { t } = useI18n();

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
      pushToast(t('component.downloadRouting.saved'));
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
  aria-label={t('component.downloadRouting.title')}>
  <div>
    <h3 class="text-base font-medium text-ink">{t('component.downloadRouting.title')}</h3>
    <p class="mt-1 text-sm text-ink-secondary">
      <LocalizedText
        message="component.downloadRouting.protocolDescription"
        links={{
          clientLink: {
            href: '/settings/downloads#download-clients',
            label: 'component.downloadRouting.externalClient',
            class: 'text-accent-text hover:underline',
          },
        }} />
    </p>
    <p class="mt-1 text-sm text-ink-secondary">
      <LocalizedText
        message="component.downloadRouting.libraryDescription"
        links={{
          settingsLink: {
            href: '/settings/libraries',
            label: 'component.downloadRouting.settingsLibraries',
            class: 'text-accent-text hover:underline',
          },
        }} />
    </p>
  </div>

  {#if error}
    <p class="text-sm text-danger">{error}</p>
  {/if}

  <Field
    label={t('component.downloadRouting.torrentReleases')}
    for="route-torrent"
    help={t('component.downloadRouting.torrentHelp')}>
    <select
      id="route-torrent"
      bind:value={torrent}
      disabled={loading}
      class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink
             focus:border-accent focus:outline-none disabled:opacity-50">
      <option value={ROUTE_EMBEDDED}>{t('component.downloadRouting.builtInEngine')}</option>
      {#each torrentClients as client (client.id)}
        <option value={String(client.id)}>{client.name}</option>
      {/each}
    </select>
  </Field>

  <Field
    label={t('component.downloadRouting.usenetReleases')}
    for="route-usenet">
    <select
      id="route-usenet"
      bind:value={usenet}
      disabled={loading}
      class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink
             focus:border-accent focus:outline-none disabled:opacity-50">
      <option value="">{t('component.downloadRouting.builtInEngine')}</option>
      {#each usenetClients as client (client.id)}
        <option value={String(client.id)}>{client.name}</option>
      {/each}
    </select>
  </Field>

  <p class="-mt-2 text-sm text-ink-secondary">
    <LocalizedText
      message="component.downloadRouting.usenetDescription"
      links={{
        settingsLink: {
          href: '/settings/downloads#usenet-servers',
          label: 'component.downloadRouting.settingsUsenetServers',
          class: 'text-accent-text hover:underline',
        },
      }} />
  </p>

  <Button variant="primary" class="self-start" disabled={loading || saving} onclick={save}>
    <Icon name="check" size={14} />
    {saving ? t('component.actions.saving') : t('component.downloadRouting.save')}
  </Button>
</section>
