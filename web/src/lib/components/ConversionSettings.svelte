<script lang="ts">
  /** Global output choices for conversions that the active TV profile requires. */
  import {
    SETTING_CONVERT_AUDIO_BITRATE_KBPS,
    SETTING_CONVERT_VIDEO_CRF,
    SETTING_CONVERT_VIDEO_PRESET,
    type Settings,
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

  const PRESETS = [
    { value: 'ultrafast', label: 'Ultra fast' },
    { value: 'superfast', label: 'Super fast' },
    { value: 'veryfast', label: 'Very fast' },
    { value: 'faster', label: 'Faster' },
    { value: 'fast', label: 'Fast' },
    { value: 'medium', label: 'Medium' },
    { value: 'slow', label: 'Slow' },
    { value: 'slower', label: 'Slower' },
    { value: 'veryslow', label: 'Very slow' },
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
  title="Conversion output"
  description="Controls required re-encoding for the active TV compatibility target.">
  {#snippet action()}
    <Button variant="primary" size="sm" disabled={saving || !canSave} onclick={save}>
      <Icon name="check" size={14} />
      {saving ? 'Saving...' : 'Save changes'}
    </Button>
  {/snippet}

  <Field
    label="Encoding speed"
    for="convert-video-preset"
    help="Faster presets finish sooner. Slower presets can make smaller files. Very fast is the default.">
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
    label="Video quality (CRF)"
    for="convert-video-crf"
    help="Lower CRF means higher quality and larger files. Choose a whole number from 0 to 51; 20 is the default."
    error={normalizedCrf === null ? 'Enter a whole number from 0 to 51.' : null}>
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
    label="AAC bitrate"
    for="convert-audio-bitrate-kbps"
    help="Used only when audio must be re-encoded to AAC. Choose 64 to 512 kb/s; 192 is the default."
    error={normalizedAudioBitrateKbps === null ? 'Enter a whole number from 64 to 512.' : null}>
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
    Container-only conversions copy streams.
    A running conversion keeps the settings it started with.
  </p>
</SettingsCard>
