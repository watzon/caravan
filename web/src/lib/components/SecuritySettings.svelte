<script lang="ts">
  /**
   * Security (SPEC §11, PLAN phase 5 task 5): the optional single-user password
   * and the API key external tools authenticate with.
   *
   * The password is not a settings key the form can PUT: setting it needs the
   * current one, and the plaintext must never round-trip, so it has its own
   * endpoint. The hash is never returned by GET /settings at all — whether a
   * password exists is read from system status.
   */
  import { api, errorText } from '../api/client';
  import { SETTING_API_KEY, type Settings } from '../api/types';
  import Banner from './Banner.svelte';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';
  import TextInput from './TextInput.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { system } from '../state/system.svelte';

  interface Props {
    settings: Settings;
  }

  let { settings }: Props = $props();

  // The saved key until this screen regenerates one, then the new key: a
  // regeneration is immediate, so re-reading /settings just to see it would be
  // a round trip for a value the response already carried.
  let regenerated = $state<string | null>(null);
  let apiKey = $derived(regenerated ?? settings[SETTING_API_KEY] ?? '');
  let currentPassword = $state('');
  let newPassword = $state('');
  let busy = $state(false);

  let passwordSet = $derived(system.status?.password_set === true);
  let publicBind = $derived(system.status?.listening_publicly === true);

  async function savePassword() {
    busy = true;
    try {
      await api.setPassword(currentPassword, newPassword);
      currentPassword = '';
      newPassword = '';
      await system.refresh();
      pushToast(passwordSet ? 'Password updated.' : 'Password set.', 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busy = false;
    }
  }

  async function clearPassword() {
    busy = true;
    try {
      await api.setPassword(currentPassword, '');
      currentPassword = '';
      newPassword = '';
      await system.refresh();
      pushToast('Password cleared. Caravan no longer asks for a login.', 'warning');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busy = false;
    }
  }

  async function regenerateKey() {
    busy = true;
    try {
      const result = await api.regenerateAPIKey();
      regenerated = result.api_key;
      pushToast('API key regenerated. The old key stopped working.', 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busy = false;
    }
  }
</script>

<section class="flex flex-col gap-6">
  {#if publicBind && !passwordSet}
    <Banner
      tone="warning"
      icon="warning"
      title="Listening on every interface without a password"
      message="Anyone on this network can reach Caravan and change its settings. Setting a password here closes that." />
  {/if}

  <div class="flex flex-col gap-4">
    <div>
      <h2 class="font-display text-base font-semibold text-ink">Password</h2>
      <p class="mt-1 text-sm text-ink-secondary">
        {passwordSet
          ? 'Caravan asks for this password before showing the library. Changing it signs out every other browser.'
          : 'Optional. With no password, anyone who can reach this server can use it.'}
      </p>
    </div>

    {#if passwordSet}
      <Field
        label="Current password"
        for="security-current-password"
        help="Required to change or clear the password.">
        <TextInput
          id="security-current-password"
          bind:value={currentPassword}
          type="password"
          placeholder="•••••" />
      </Field>
    {/if}

    <Field
      label={passwordSet ? 'New password' : 'Password'}
      for="security-new-password"
      help="At least 8 characters. Stored as an argon2id hash and never returned by the API.">
      <TextInput
        id="security-new-password"
        bind:value={newPassword}
        type="password"
        placeholder="•••••" />
    </Field>

    <div class="flex flex-wrap gap-2">
      <Button
        variant="primary"
        disabled={busy || newPassword.trim().length < 8}
        onclick={savePassword}>
        <Icon name="check" size={14} />
        {busy ? 'Saving…' : passwordSet ? 'Change password' : 'Set password'}
      </Button>
      {#if passwordSet}
        <Button variant="danger" disabled={busy} onclick={clearPassword}>
          <Icon name="close" size={14} />
          Clear password
        </Button>
      {/if}
    </div>
  </div>

  <div class="flex flex-col gap-4 border-t border-border pt-6">
    <div>
      <h2 class="font-display text-base font-semibold text-ink">API key</h2>
      <p class="mt-1 text-sm text-ink-secondary">
        For external tools and the calendar feed. Send it as the
        <code class="font-mono">X-Api-Key</code> header or an
        <code class="font-mono">apikey</code> query parameter. Regenerating revokes the old one
        immediately.
      </p>
    </div>

    <Field label="Key" for="security-api-key">
      <TextInput id="security-api-key" value={apiKey} mono readonly placeholder="No key yet" />
    </Field>

    <Button variant="secondary" class="self-start" disabled={busy} onclick={regenerateKey}>
      <Icon name="refresh" size={14} />
      {apiKey === '' ? 'Generate key' : 'Regenerate key'}
    </Button>
  </div>
</section>
