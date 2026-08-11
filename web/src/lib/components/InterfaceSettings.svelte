<script lang="ts">
  import { onMount } from 'svelte';
  import { useI18n } from '../i18n.svelte';
  import Field from './Field.svelte';
  import SettingsCard from './SettingsCard.svelte';
  import {
    readDisplayPreferences,
    saveDisplayPreferences,
    type MotionPreference,
    type ThemePreference,
  } from '../displayPreferences';

  const SELECT_CLASS = 'h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink focus:border-accent focus:outline-none';
  const THEME_HELP_ID = 'display-theme-help';
  const MOTION_HELP_ID = 'display-motion-help';

  const { t } = useI18n();
  let theme = $state<ThemePreference>('system');
  let motion = $state<MotionPreference>('system');

  let saved = $state(false);
  onMount(() => {
    const preferences = readDisplayPreferences();
    theme = preferences.theme;
    motion = preferences.motion;
  });

  function savePreferences() {
    saved = false;
    saveDisplayPreferences({ theme, motion });
    saved = true;
  }

  function updateTheme(value: ThemePreference) {
    theme = value;
    savePreferences();
  }

  function updateMotion(value: MotionPreference) {
    motion = value;
    savePreferences();
  }
</script>

<section id="interface" class="flex flex-col gap-6" aria-labelledby="interface-heading">
  <h2 id="interface-heading" class="sr-only">{t('component.interface.title')}</h2>

  <SettingsCard
    title={t('component.interface.appearance')}
    description={t('component.interface.description')}>
    <div class="grid gap-4 sm:grid-cols-2">
      <Field label={t('component.interface.theme')} for="display-theme">
        <select
          id="display-theme"
          aria-describedby={THEME_HELP_ID}
          value={theme}
          class={SELECT_CLASS}
          onchange={(event) => updateTheme(event.currentTarget.value as ThemePreference)}>
          <option value="system">{t('component.interface.theme.system')}</option>
          <option value="dark">{t('component.interface.theme.dark')}</option>
          <option value="light">{t('component.interface.theme.light')}</option>
        </select>
        <p id={THEME_HELP_ID} class="text-sm text-ink-secondary">
          {t('component.interface.theme.help')}
        </p>
      </Field>

      <Field label={t('component.interface.motion')} for="display-motion">
        <select
          id="display-motion"
          aria-describedby={MOTION_HELP_ID}
          value={motion}
          class={SELECT_CLASS}
          onchange={(event) => updateMotion(event.currentTarget.value as MotionPreference)}>
          <option value="system">{t('component.interface.motion.system')}</option>
          <option value="reduced">{t('component.interface.motion.reduced')}</option>
        </select>
        <p id={MOTION_HELP_ID} class="text-sm text-ink-secondary">
          {t('component.interface.motion.help')}
        </p>
      </Field>
    </div>
    {#if saved}
      <p class="text-sm text-success" aria-live="polite">{t('component.interface.saved')}</p>
    {/if}
  </SettingsCard>
</section>
