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
  import { useI18n } from '../i18n.svelte';
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
  const { t } = useI18n();

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
  title={t('component.concurrency.title')}
  description={t('component.concurrency.description')}>
  {#snippet action()}
    <Button variant="primary" size="sm" disabled={saving || !canSave || !hasChanges} onclick={save}>
      <Icon name="check" size={14} />
      {saving ? t('component.actions.saving') : !canSave ? t('component.actions.fixErrors') : !hasChanges ? t('component.actions.noChanges') : t('component.actions.saveChanges')}
    </Button>
  {/snippet}

  <div class="flex flex-col gap-4">
    <Field
      label={t('component.concurrency.maxDownloads')}
      for="max-concurrent-downloads"
      help={t('component.concurrency.maxDownloadsHelp')}
      error={normalizedGlobal === null ? t('component.validation.nonNegativeInteger') : null}>
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
    <h2 class="micro-label">{t('component.concurrency.perEngine')}</h2>
    <Field
      label={t('component.concurrency.torrentEngine')}
      for="embedded-torrent-max-concurrent"
      help={t('component.concurrency.torrentHelp')}
      error={normalizedTorrent === null ? t('component.validation.nonNegativeInteger') : null}>
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
      label={t('component.concurrency.usenetEngine')}
      for="embedded-usenet-max-concurrent"
      help={t('component.concurrency.usenetHelp')}
      error={normalizedUsenet === null ? t('component.validation.nonNegativeInteger') : null}>
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
