<script lang="ts">
  /** Global output choices for conversions that an item's playback target requires. */
  import {
    SETTING_CONVERT_AUDIO_BITRATE_KBPS,
    SETTING_CONVERT_VIDEO_CRF,
    SETTING_CONVERT_VIDEO_PRESET,
    type Settings,
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

  const { t } = useI18n();

  const PRESETS = [
    { value: 'ultrafast', label: t('component.conversionSettings.preset.ultrafast') },
    { value: 'superfast', label: t('component.conversionSettings.preset.superfast') },
    { value: 'veryfast', label: t('component.conversionSettings.preset.veryfast') },
    { value: 'faster', label: t('component.conversionSettings.preset.faster') },
    { value: 'fast', label: t('component.conversionSettings.preset.fast') },
    { value: 'medium', label: t('component.conversionSettings.preset.medium') },
    { value: 'slow', label: t('component.conversionSettings.preset.slow') },
    { value: 'slower', label: t('component.conversionSettings.preset.slower') },
    { value: 'veryslow', label: t('component.conversionSettings.preset.veryslow') },
  ] as const;

  let { settings, saving = false, onsave }: Props = $props();

  let preset = $state('veryfast');
  let crf = $state('20');
  let audioBitrateKbps = $state('192');
  $effect(() => {
    preset = settings[SETTING_CONVERT_VIDEO_PRESET] ?? 'veryfast';
    crf = settings[SETTING_CONVERT_VIDEO_CRF] ?? '20';
    audioBitrateKbps = settings[SETTING_CONVERT_AUDIO_BITRATE_KBPS] ?? '192';
  });

  function normalizedInteger(
    value: string | number | null | undefined,
    min: number,
    max: number,
  ): string | null {
    if (value === null || value === undefined) return null;
    const text = String(value).trim();
    if (!/^\d+$/.test(text)) return null;

    const number = Number(text);
    if (!Number.isSafeInteger(number) || number < min || number > max) return null;
    return String(number);
  }

  let normalizedCrf = $derived(normalizedInteger(crf, 0, 51));
  let normalizedAudioBitrateKbps = $derived(normalizedInteger(audioBitrateKbps, 64, 512));
  let canSave = $derived(
    PRESETS.some((option) => option.value === preset) &&
      normalizedCrf !== null &&
      normalizedAudioBitrateKbps !== null,
  );

  async function save() {
    if (!canSave || normalizedCrf === null || normalizedAudioBitrateKbps === null) return;

    await onsave({
      [SETTING_CONVERT_VIDEO_PRESET]: preset,
      [SETTING_CONVERT_VIDEO_CRF]: normalizedCrf,
      [SETTING_CONVERT_AUDIO_BITRATE_KBPS]: normalizedAudioBitrateKbps,
    });
  }
</script>

<SettingsCard
  title={t('component.conversionSettings.title')}
  description={t('component.conversionSettings.description')}>
  {#snippet action()}
    <Button variant="primary" size="sm" disabled={saving || !canSave} onclick={save}>
      <Icon name="check" size={14} />
      {saving ? t('component.actions.saving') : t('component.actions.saveChanges')}
    </Button>
  {/snippet}

  <Field
    label={t('component.conversionSettings.encodingSpeed')}
    for="convert-video-preset"
    help={t('component.conversionSettings.encodingSpeedHelp')}>
    <select
      id="convert-video-preset"
      bind:value={preset}
      class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink focus:border-accent focus:outline-none">
      {#each PRESETS as option (option.value)}
        <option value={option.value}>{option.label}</option>
      {/each}
    </select>
  </Field>

  <Field
    label={t('component.conversionSettings.videoQuality')}
    for="convert-video-crf"
    help={t('component.conversionSettings.videoQualityHelp')}
    error={normalizedCrf === null ? t('component.conversionSettings.videoQualityError') : null}>
    <input
      id="convert-video-crf"
      type="number"
      min="0"
      max="51"
      step="1"
      aria-invalid={normalizedCrf === null}
      bind:value={crf}
      class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
  </Field>
  <Field
    label={t('component.conversionSettings.audioBitrate')}
    for="convert-audio-bitrate-kbps"
    help={t('component.conversionSettings.audioBitrateHelp')}
    error={normalizedAudioBitrateKbps === null ? t('component.conversionSettings.audioBitrateError') : null}>
    <input
      id="convert-audio-bitrate-kbps"
      type="number"
      min="64"
      max="512"
      step="1"
      aria-invalid={normalizedAudioBitrateKbps === null}
      bind:value={audioBitrateKbps}
      class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
  </Field>

  <p class="text-sm text-ink-secondary">
    {t('component.conversionSettings.note')}
  </p>
</SettingsCard>
