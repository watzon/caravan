<script lang="ts">
  import { onMount } from 'svelte';
  import Field from './Field.svelte';
  import SettingsCard from './SettingsCard.svelte';
  import {
    readDisplayPreferences,
    saveDisplayPreferences,
    type MotionPreference,
    type ThemePreference,
  } from '../displayPreferences';

  const SELECT_CLASS = 'h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink focus:border-accent focus:outline-none';

  let theme = $state<ThemePreference>('system');
  let motion = $state<MotionPreference>('system');

  onMount(() => {
    const preferences = readDisplayPreferences();
    theme = preferences.theme;
    motion = preferences.motion;
  });

  function updateTheme(value: ThemePreference) {
    theme = value;
    saveDisplayPreferences({ theme, motion });
  }

  function updateMotion(value: MotionPreference) {
    motion = value;
    saveDisplayPreferences({ theme, motion });
  }
</script>

<section id="interface" class="flex flex-col gap-6" aria-labelledby="interface-heading">
  <h2 id="interface-heading" class="sr-only">Interface</h2>

  <SettingsCard
    title="Appearance"
    description="These preferences apply only to this browser, so each person and device can choose its own display.">
    <div class="grid gap-4 sm:grid-cols-2">
      <Field label="Theme" for="display-theme" help="System follows this device's light or dark appearance.">
        <select
          id="display-theme"
          value={theme}
          class={SELECT_CLASS}
          onchange={(event) => updateTheme(event.currentTarget.value as ThemePreference)}>
          <option value="system">System</option>
          <option value="dark">Dark</option>
          <option value="light">Light</option>
        </select>
      </Field>

      <Field label="Motion" for="display-motion" help="Reduce interface transitions and animated feedback on this browser.">
        <select
          id="display-motion"
          value={motion}
          class={SELECT_CLASS}
          onchange={(event) => updateMotion(event.currentTarget.value as MotionPreference)}>
          <option value="system">Follow system</option>
          <option value="reduced">Reduce motion</option>
        </select>
      </Field>
    </div>
  </SettingsCard>
</section>
