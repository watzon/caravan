<script lang="ts">
  /**
   * Security (SPEC §11): the calling account's own password, and the API key
   * external tools authenticate with.
   *
   * The password is not a settings key the form can PUT: changing it needs the
   * current one, and the plaintext must never round-trip, so it has its own
   * endpoint. It only ever touches the caller — creating accounts and resetting
   * somebody else's password live under Users — and it needs an account to act
   * on, so an open Caravan is pointed at Users instead.
   */
  import { api, errorText } from '../api/client';
  import { SETTING_API_KEY, type Settings } from '../api/types';
  import Banner from './Banner.svelte';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';
  import TextInput from './TextInput.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { session } from '../state/session.svelte';
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

  /** `password_set` means "this server has at least one account" (internal/api). */
  let hasAccounts = $derived(system.status?.password_set === true);
  let publicBind = $derived(system.status?.listening_publicly === true);
  /** No account behind this browser: an open server, or the API key. */
  let signedIn = $derived(session.username !== '');

  async function savePassword() {
    busy = true;
    try {
      await api.setPassword(currentPassword, newPassword);
      currentPassword = '';
      newPassword = '';
      pushToast('Password updated. Your other browsers are signed out.', 'success');
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
  {#if publicBind && !hasAccounts}
    <Banner
      tone="warning"
      icon="warning"
      title="Listening on every interface without a password"
      message="Anyone on this network can reach Caravan and change its settings. Adding the first account under Users closes that." />
  {/if}

  <div class="flex flex-col gap-4">
    <div>
      <h2 class="font-display text-base font-semibold text-ink">Your password</h2>
      <p class="mt-1 text-sm text-ink-secondary">
        {signedIn
          ? 'Changes your own password and signs out every other browser you are signed in on. Nobody else is affected.'
          : 'This browser is not signed in as an account, so there is no password of yours to change. Add the first account under Users.'}
      </p>
    </div>

    {#if signedIn}
      <Field
        label="Current password"
        for="security-current-password"
        help="Proves it is you and not somebody at your unlocked screen.">
        <TextInput
          id="security-current-password"
          bind:value={currentPassword}
          type="password"
          placeholder="•••••" />
      </Field>

      <Field
        label="New password"
        for="security-new-password"
        help="At least 8 characters. Stored as an argon2id hash and never returned by the API.">
        <TextInput
          id="security-new-password"
          bind:value={newPassword}
          type="password"
          placeholder="•••••" />
      </Field>

      <Button
        variant="primary"
        class="self-start"
        disabled={busy || currentPassword === '' || newPassword.trim().length < 8}
        onclick={savePassword}>
        <Icon name="check" size={14} />
        {busy ? 'Saving…' : 'Change password'}
      </Button>
    {:else}
      <Button variant="secondary" class="self-start" href="/settings/users">Open Users</Button>
    {/if}
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
