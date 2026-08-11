<script lang="ts">
  import { useI18n } from '../i18n.svelte';
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
  import { MIN_PASSWORD_LENGTH, SETTING_API_KEY, type Settings } from '../api/types';
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
      pushToast(t('component.securitySettings.passwordUpdated'), 'success');
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
      pushToast(t('component.securitySettings.apiKeyRegenerated'), 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busy = false;
    }
  }

  const { t, tp } = useI18n();
</script>

<section class="flex flex-col gap-6">
  {#if publicBind && !hasAccounts}
    <Banner
      tone="warning"
      icon="warning"
      title={t('component.securitySettings.listeningOnEveryInterfaceWithoutAPassword')}
      message={t('component.securitySettings.anyoneOnThisNetworkCanReachCaravanAndChangeItsSettingsAddingTheFirstAccountUnderUsersClosesThat')} />
  {/if}

  <div class="flex flex-col gap-4">
    <div>
      <h2 class="font-display text-base font-semibold text-ink">{t('component.securitySettings.yourPassword')}</h2>
      <p class="mt-1 text-sm text-ink-secondary">
        {signedIn
          ? t('component.securitySettings.signedInCopy')
          : t('component.securitySettings.notSignedInCopy')}
      </p>
    </div>

    {#if signedIn}
      <Field
        label={t('component.securitySettings.currentPassword')}
        for="security-current-password"
        help={t('component.securitySettings.provesItIsYouAndNotSomebodyAtYourUnlockedScreen')}>
        <TextInput
          id="security-current-password"
          bind:value={currentPassword}
          type="password"
          placeholder="•••••" />
      </Field>

      <Field
        label={t('component.securitySettings.newPassword')}
        for="security-new-password"
        help={t('component.securitySettings.newPasswordHelp', { count: MIN_PASSWORD_LENGTH })}>
        <TextInput
          id="security-new-password"
          bind:value={newPassword}
          type="password"
          placeholder="•••••" />
      </Field>

      <Button
        variant="primary"
        class="self-start"
        disabled={busy || currentPassword === '' || newPassword.length < MIN_PASSWORD_LENGTH}
        onclick={savePassword}>
        <Icon name="check" size={14} />
        {busy ? t('component.securitySettings.saving') : t('component.securitySettings.changePassword')}
      </Button>
    {:else}
      <Button variant="secondary" class="self-start" href="/settings/users">{t('component.securitySettings.openUsers')}</Button>
    {/if}
  </div>

  <div class="flex flex-col gap-4 border-t border-border pt-6">
    <div>
      <h2 class="font-display text-base font-semibold text-ink">{t('component.securitySettings.apiKey')}</h2>
      <p class="mt-1 text-sm text-ink-secondary">
        {t('component.securitySettings.apiKeyUsage', { header: 'X-Api-Key', parameter: 'apikey' })}
      </p>
    </div>

    <Field label={t('component.securitySettings.key')} for="security-api-key">
      <TextInput id="security-api-key" value={apiKey} mono readonly placeholder={t('component.securitySettings.noKeyYet')} />
    </Field>

    <Button variant="secondary" class="self-start" disabled={busy} onclick={regenerateKey}>
      <Icon name="refresh" size={14} />
      {apiKey === '' ? t('component.securitySettings.generateKey') : t('component.securitySettings.regenerateKey')}
    </Button>
  </div>
</section>
