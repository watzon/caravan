<script lang="ts">
  /**
   * The optional FlareSolverr endpoint. Local tracker definitions behind a
   * Cloudflare or DDoS-Guard challenge only work once it is set; every other
   * indexer ignores it.
   */
  import { untrack } from 'svelte';
  import { api, errorText } from '../api/client';
  import { SETTING_FLARESOLVERR_URL, type Settings } from '../api/types';
  import { useI18n } from '../i18n.svelte';
  import Banner from './Banner.svelte';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';
  import SettingsCard from './SettingsCard.svelte';
  import TextInput from './TextInput.svelte';

  interface Props {
    settings: Settings;
    saving?: boolean;
    onsave: (patch: Settings, note: string) => Promise<boolean>;
  }

  interface TestResult {
    ok: boolean;
    message: string;
  }

  let { settings, saving = false, onsave }: Props = $props();
  const { t } = useI18n();

  let url = $state(untrack(() => settings[SETTING_FLARESOLVERR_URL] ?? ''));
  let testing = $state(false);
  let result = $state<TestResult | null>(null);

  let dirty = $derived(url.trim() !== (settings[SETTING_FLARESOLVERR_URL] ?? ''));

  async function save() {
    const ok = await onsave({ [SETTING_FLARESOLVERR_URL]: url.trim() }, t('component.flaresolverr.saved'));
    if (ok) result = null;
  }

  async function test() {
    testing = true;
    result = null;
    try {
      const outcome = await api.testFlareSolverr(url.trim());
      result = { ok: true, message: t('component.flaresolverr.testOk', { version: outcome.version }) };
    } catch (err) {
      result = { ok: false, message: errorText(err) };
    } finally {
      testing = false;
    }
  }
</script>

<SettingsCard title="FlareSolverr" description={t('component.flaresolverr.description')}>
  {#snippet action()}
    <Button variant="primary" size="sm" disabled={saving || !dirty} onclick={save}>
      <Icon name="check" size={14} />
      {saving ? t('component.flaresolverr.saving') : t('component.flaresolverr.save')}
    </Button>
  {/snippet}

  <Field label={t('component.flaresolverr.url')} for="flaresolverr-url" help={t('component.flaresolverr.urlHelp')}>
    <TextInput id="flaresolverr-url" bind:value={url} mono placeholder="http://flaresolverr:8191" />
  </Field>

  <Button variant="secondary" class="self-start" disabled={testing || url.trim() === ''} onclick={test}>
    {testing ? t('component.flaresolverr.testing') : t('component.flaresolverr.test')}
  </Button>

  {#if result}
    <Banner tone={result.ok ? 'success' : 'danger'} icon={result.ok ? 'check' : 'warning'} message={result.message} />
  {/if}
</SettingsCard>
