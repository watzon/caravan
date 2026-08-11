<script lang="ts">
  import { useI18n } from '../i18n.svelte';
  /**
   * The Settings → Metadata card for a provider that needs a key.
   *
   * Extracted from the TMDB card the moment TheTVDB made it the second of its
   * kind. Everything a key-holding provider needs is the same shape — a health
   * badge, a banner naming the fault, a write-only field, Save/Clear/Test — and
   * the only per-provider facts are the id, the display name and the settings
   * keys, so those are props and the rest is one copy.
   *
   * The card owns the typed key, the PIN and the test verdict: those are about
   * the form rather than about the settings row, and two cards must not share
   * them. Persisted state stays the pane's — `settings` and `saving` come down,
   * `onsave` goes up — because a settings PUT replaces the whole row.
   */
  import { api, errorText } from '../api/client';
  import type { Settings } from '../api/types';
  import Badge from './Badge.svelte';
  import Banner from './Banner.svelte';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';
  import SettingsCard from './SettingsCard.svelte';
  import TextInput from './TextInput.svelte';
  import { providerStateOf } from '../credentials';
  import { system } from '../state/system.svelte';

  /**
   * A second credential field beside the key, for a provider whose login
   * consumes a pair. TheTVDB is the only one today: its PIN belongs to a
   * user-supported subscription and is absent for a licensed key.
   */
  interface ExtraField {
    /** The settings key it is written to. */
    setting: string;
    /** The input's DOM id, so its label can point at it. */
    inputId: string;
    label: string;
    help: string;
  }

  interface Props {
    /** core.ProviderDescriptor.ID — the badge's subject and Test's `provider`. */
    providerId: string;
    /** The card heading, and the name every sentence about it says. */
    title: string;
    description: string;
    /** The key input's DOM id, so a label addresses one card's field. */
    inputId: string;
    /** The settings key the typed value is written to. */
    keySetting: string;
    /** The public "a key is stored" flag beside it (`<setting>_set`). */
    keySetSetting: string;
    /** What a missing key costs, in this provider's own terms. */
    absentMessage: string;
    settings: Settings;
    saving: boolean;
    onsave: (patch: Settings, note: string) => Promise<boolean>;
    pin?: ExtraField;
  }

  let {
    providerId,
    title,
    description,
    inputId,
    keySetting,
    keySetSetting,
    absentMessage,
    settings,
    saving,
    onsave,
    pin,
  }: Props = $props();

  let keyValue = $state('');
  let pinValue = $state('');
  let test = $state<{ ok: boolean; message: string } | null>(null);
  let testing = $state(false);

  let state = $derived(providerStateOf(system.status, providerId));
  let reason = $derived(system.credential(providerId).reason);
  let hasKey = $derived(settings[keySetSetting] === 'true');
  let badge = $derived(
    state === 'invalid'
      ? { tone: 'danger' as const, label: t('component.providerKeyCard.keyRejected') }
      : state === 'absent'
        ? { tone: 'warning' as const, label: t('component.providerKeyCard.noKey') }
        : { tone: 'success' as const, label: t('component.providerKeyCard.connected') },
  );
  /** Nothing typed is nothing to write: a blank field keeps what is stored. */
  let nothingTyped = $derived(keyValue.trim() === '' && pinValue.trim() === '');

  async function saveTyped() {
    const patch: Settings = {};
    if (keyValue.trim() !== '') patch[keySetting] = keyValue.trim();
    if (pin && pinValue.trim() !== '') patch[pin.setting] = pinValue.trim();
    await onsave(patch, saveNote(patch));
  }

  /** Names what was written, so a PIN-only save does not claim a key. */
  function saveNote(patch: Settings): string {
    if (pin && patch[pin.setting] !== undefined) {
      return patch[keySetting] === undefined
        ? t('component.providerKeyCard.pinSaved', { title, pin: pin.label })
        : t('component.providerKeyCard.credentialsSaved', { title });
    }
    return t('component.providerKeyCard.apiKeySaved', { title });
  }

  /**
   * Clear takes the pair, not the key alone. A stored PIN outliving the key it
   * logs in with is a half-credential nothing can use and the card cannot show.
   */
  async function clearStored() {
    const patch: Settings = { [keySetting]: '' };
    if (pin) patch[pin.setting] = '';
    await onsave(patch, pin ? t('component.providerKeyCard.credentialsCleared') : t('component.providerKeyCard.apiKeyCleared'));
  }

  async function testKey() {
    testing = true;
    try {
      await api.testMetadataKey(keyValue.trim(), providerId);
      test = { ok: true, message: t('component.providerKeyCard.keyAccepted', { title }) };
    } catch (err) {
      test = { ok: false, message: errorText(err) };
    } finally {
      testing = false;
      await system.refresh();
    }
  }

  const { t, tp } = useI18n();
</script>

<SettingsCard {title} {description}>
  {#snippet action()}
    <Badge tone={badge.tone}>{badge.label}</Badge>
  {/snippet}
  {#if state !== 'ok'}
    <Banner
      tone="warning"
      icon="warning"
      title={state === 'invalid' ? t('component.providerKeyCard.keyRejectedDetail', { title }) : t('component.providerKeyCard.noApiKeyYet', { title })}
      message={state === 'invalid'
        ? reason || t('component.providerKeyCard.storedKeyRefused')
        : absentMessage} />
  {/if}

  <Field
    label={t('component.providerKeyCard.apiKeyLabel', { title })}
    for={inputId}
    help={t('component.providerKeyCard.storedInTheDatabaseNeverInCaravanYamlOrLogs')}
    error={test && !test.ok ? test.message : null}>
    <TextInput
      id={inputId}
      bind:value={keyValue}
      type="password"
      mono
      placeholder="•••••"
      oninput={() => (test = null)} />
  </Field>
  {#if hasKey}
    <p class="-mt-2 text-sm text-ink-secondary">{t('component.providerKeyCard.aKeyIsStoredLeaveBlankToKeepIt')}</p>
  {/if}

  {#if pin}
    <Field label={pin.label} for={pin.inputId} help={pin.help}>
      <TextInput id={pin.inputId} bind:value={pinValue} type="password" mono placeholder="•••••" />
    </Field>
  {/if}

  {#if test?.ok}
    <p class="-mt-2 text-sm text-success">✓ {test.message}</p>
  {/if}

  <div class="flex flex-wrap items-center gap-2">
    <Button variant="primary" disabled={saving || nothingTyped} onclick={saveTyped}>
      <Icon name="check" size={14} />
      {saving ? t('component.providerKeyCard.saving') : t('component.providerKeyCard.save')}
    </Button>
    <Button variant="secondary" disabled={!hasKey || saving} onclick={clearStored}>
      {t('component.providerKeyCard.clear')}
    </Button>
    <Button variant="secondary" disabled={testing} onclick={testKey}>
      {testing ? t('component.providerKeyCard.testing') : t('component.providerKeyCard.test')}
    </Button>
  </div>
</SettingsCard>
