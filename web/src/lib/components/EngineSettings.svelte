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
  import { useI18n } from '../i18n.svelte';
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

  const { t } = useI18n();

  let { settings, saving = false, onsave }: Props = $props();

  let listenPort = $state('');
  let maxConnections = $state('');
  let maxDownKbps = $state('');
  let maxUpKbps = $state('');
  let seedRatio = $state('');
  let seedDays = $state('');
  let restartNotice = $state(false);
  let baseline = $state({
    listenPort: '0',
    maxConnections: '0',
    maxDownKbps: '0',
    maxUpKbps: '0',
    seedRatio: '0',
    seedDays: '0',
  });
  // Deliberately a plain box rather than $state: this marks which settings
  // object the fields were last filled from, and tracking it would make the
  // effect below depend on something it writes on every run — which trips
  // Svelte's update-depth guard.
  const filledFrom: { settings: Settings | null } = { settings: null };

  function normalizedInteger(
    value: string | number | null | undefined,
    min: number,
    max = Number.MAX_SAFE_INTEGER,
  ): string | null {
    const text = value === null || value === undefined ? '' : String(value).trim();
    if (text === '') return min === 0 ? '0' : null;
    if (!/^\d+$/.test(text)) return null;

    const number = Number(text);
    if (!Number.isSafeInteger(number) || number < min || number > max) return null;
    return String(number);
  }

  function normalizedDecimal(value: string | number | null | undefined): string | null {
    const text = value === null || value === undefined ? '' : String(value).trim();
    if (text === '') return '0';
    if (!/^\d+(?:\.\d+)?$/.test(text)) return null;

    const number = Number(text);
    if (!Number.isFinite(number) || number < 0) return null;
    return String(number);
  }

  $effect(() => {
    if (filledFrom.settings === settings) return;
    filledFrom.settings = settings;
    const loaded = {
      listenPort: normalizedInteger(settings[SETTING_ENGINE_LISTEN_PORT], 0, 65535) ?? '0',
      maxConnections: normalizedInteger(settings[SETTING_ENGINE_MAX_CONNECTIONS], 0) ?? '0',
      maxDownKbps: normalizedInteger(settings[SETTING_ENGINE_MAX_DOWN_KBPS], 0) ?? '0',
      maxUpKbps: normalizedInteger(settings[SETTING_ENGINE_MAX_UP_KBPS], 0) ?? '0',
      seedRatio: normalizedDecimal(settings[SETTING_ENGINE_SEED_RATIO]) ?? '0',
      seedDays: normalizedInteger(settings[SETTING_ENGINE_SEED_DAYS], 0) ?? '0',
    };
    baseline = loaded;
    listenPort = loaded.listenPort;
    maxConnections = loaded.maxConnections;
    maxDownKbps = loaded.maxDownKbps;
    maxUpKbps = loaded.maxUpKbps;
    seedRatio = loaded.seedRatio;
    seedDays = loaded.seedDays;
  });

  let normalizedListenPort = $derived(normalizedInteger(listenPort, 0, 65535));
  let normalizedMaxConnections = $derived(normalizedInteger(maxConnections, 0));
  let normalizedMaxDownKbps = $derived(normalizedInteger(maxDownKbps, 0));
  let normalizedMaxUpKbps = $derived(normalizedInteger(maxUpKbps, 0));
  let normalizedSeedRatio = $derived(normalizedDecimal(seedRatio));
  let normalizedSeedDays = $derived(normalizedInteger(seedDays, 0));
  let canSave = $derived(
    normalizedListenPort !== null &&
      normalizedMaxConnections !== null &&
      normalizedMaxDownKbps !== null &&
      normalizedMaxUpKbps !== null &&
      normalizedSeedRatio !== null &&
      normalizedSeedDays !== null,
  );
  let hasChanges = $derived(
    canSave &&
      (normalizedListenPort !== baseline.listenPort ||
        normalizedMaxConnections !== baseline.maxConnections ||
        normalizedMaxDownKbps !== baseline.maxDownKbps ||
        normalizedMaxUpKbps !== baseline.maxUpKbps ||
        normalizedSeedRatio !== baseline.seedRatio ||
        normalizedSeedDays !== baseline.seedDays),
  );

  async function save() {
    if (
      !canSave ||
      !hasChanges ||
      normalizedListenPort === null ||
      normalizedMaxConnections === null ||
      normalizedMaxDownKbps === null ||
      normalizedMaxUpKbps === null ||
      normalizedSeedRatio === null ||
      normalizedSeedDays === null
    ) {
      return;
    }

    const patch = {
      [SETTING_ENGINE_LISTEN_PORT]: normalizedListenPort,
      [SETTING_ENGINE_MAX_CONNECTIONS]: normalizedMaxConnections,
      [SETTING_ENGINE_MAX_DOWN_KBPS]: normalizedMaxDownKbps,
      [SETTING_ENGINE_MAX_UP_KBPS]: normalizedMaxUpKbps,
      [SETTING_ENGINE_SEED_RATIO]: normalizedSeedRatio,
      [SETTING_ENGINE_SEED_DAYS]: normalizedSeedDays,
    };
    const changedAtRestart =
      normalizedListenPort !== baseline.listenPort ||
      normalizedMaxConnections !== baseline.maxConnections;
    const saved = await onsave(patch);
    if (saved) {
      baseline = {
        listenPort: normalizedListenPort,
        maxConnections: normalizedMaxConnections,
        maxDownKbps: normalizedMaxDownKbps,
        maxUpKbps: normalizedMaxUpKbps,
        seedRatio: normalizedSeedRatio,
        seedDays: normalizedSeedDays,
      };
      restartNotice = changedAtRestart;
    }
  }
</script>

<SettingsCard
  title={t('component.engine.title')}
  description={t('component.engine.description')}>
  {#snippet action()}
    <Button variant="primary" size="sm" disabled={saving || !canSave || !hasChanges} onclick={save}>
      <Icon name="check" size={14} />
      {saving ? t('component.actions.saving') : !canSave ? t('component.actions.fixErrors') : !hasChanges ? t('component.actions.noChanges') : t('component.actions.saveChanges')}
    </Button>
  {/snippet}

  <div data-settings-advanced class="flex flex-col gap-4">
    <h2 class="micro-label">{t('component.engine.connection')}</h2>
    <Field
      label={t('component.engine.listenPort')}
      for="engine-listen-port"
      help={t('component.engine.listenPortHelp')}
      error={normalizedListenPort === null ? t('component.engine.listenPortError') : null}>
      <input
        id="engine-listen-port"
        type="number"
        min="0"
        max="65535"
        step="1"
        aria-invalid={normalizedListenPort === null}
        bind:value={listenPort}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>
    <Field
      label={t('component.engine.maxConnections')}
      for="engine-max-connections"
      help={t('component.engine.maxConnectionsHelp')}
      error={normalizedMaxConnections === null ? t('component.validation.nonNegativeInteger') : null}>
      <input
        id="engine-max-connections"
        type="number"
        min="0"
        step="1"
        aria-invalid={normalizedMaxConnections === null}
        bind:value={maxConnections}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>
  </div>

  <div data-settings-advanced class="flex flex-col gap-4">
    <h2 class="micro-label">{t('component.engine.globalRateLimits')}</h2>
    <Field
      label={t('component.engine.downloadLimit')}
      for="engine-max-down-kbps"
      help={t('component.engine.rateLimitHelp')}
      error={normalizedMaxDownKbps === null ? t('component.validation.nonNegativeInteger') : null}>
      <input
        id="engine-max-down-kbps"
        type="number"
        min="0"
        step="1"
        aria-invalid={normalizedMaxDownKbps === null}
        bind:value={maxDownKbps}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>
    <Field
      label={t('component.engine.uploadLimit')}
      for="engine-max-up-kbps"
      help={t('component.engine.rateLimitHelp')}
      error={normalizedMaxUpKbps === null ? t('component.validation.nonNegativeInteger') : null}>
      <input
        id="engine-max-up-kbps"
        type="number"
        min="0"
        step="1"
        aria-invalid={normalizedMaxUpKbps === null}
        bind:value={maxUpKbps}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>
  </div>

  <div data-settings-advanced class="flex flex-col gap-4">
    <h2 class="micro-label">{t('component.engine.seedingTargets')}</h2>
    <Field
      label={t('component.engine.stopAtRatio')}
      for="engine-seed-ratio"
      help={t('component.engine.targetHelp')}
      error={normalizedSeedRatio === null ? t('component.engine.decimalError') : null}>
      <input
        id="engine-seed-ratio"
        type="number"
        min="0"
        step="0.01"
        aria-invalid={normalizedSeedRatio === null}
        bind:value={seedRatio}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>
    <Field
      label={t('component.engine.stopAfterDays')}
      for="engine-seed-days"
      help={t('component.engine.targetHelp')}
      error={normalizedSeedDays === null ? t('component.validation.nonNegativeInteger') : null}>
      <input
        id="engine-seed-days"
        type="number"
        min="0"
        step="1"
        aria-invalid={normalizedSeedDays === null}
        bind:value={seedDays}
        class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
    </Field>
    <Banner
      tone="info"
      icon="warning"
      title={t('component.engine.portableDriveTitle')}
      message={t('component.engine.portableDriveMessage')} />
  </div>

  {#if restartNotice}
    <p class="text-sm text-ink-muted">{t('component.engine.portChangeNotice')}</p>
  {/if}
</SettingsCard>
