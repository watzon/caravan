<script lang="ts">
  /**
   * Settings → Indexers owner pack card. Preview and install stay in this
   * component's memory; the token and public key never go to storage, the URL,
   * or the live status text.
   */
  import { onDestroy, onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { DefinitionPackPreview, DefinitionPackRevision } from '../api/types';
  import { useI18n } from '../i18n.svelte';
  import Badge from './Badge.svelte';
  import Banner from './Banner.svelte';
  import Button from './Button.svelte';
  import Field from './Field.svelte';
  import LoadError from './LoadError.svelte';
  import Modal from './Modal.svelte';
  import SettingsCard from './SettingsCard.svelte';
  import TextInput from './TextInput.svelte';

  const { t } = useI18n();

  type Phase = 'idle' | 'ready' | 'previewing' | 'previewed' | 'installing';

  let revisions = $state<DefinitionPackRevision[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let formError = $state<string | null>(null);
  let phase = $state<Phase>('idle');
  let archiveInput = $state<HTMLInputElement | undefined>();
  let archive = $state<File | null>(null);
  let signerKeyId = $state('');
  let publicKey = $state('');
  let preview = $state<DefinitionPackPreview | null>(null);
  let accepted = $state(false);
  let restartRequired = $state(false);
  let now = $state(Date.now());
  let busyAction = $state<'activate' | 'rollback' | null>(null);
  let busyRef = $state('');
  let modalOpen = $state(false);
  let formGeneration = 0;

  let tokenExpired = $derived.by(() => {
    if (!preview) return false;
    const expires = Date.parse(preview.expires_at);
    return Number.isNaN(expires) || expires <= now;
  });
  let canPreview = $derived(
    (phase === 'ready' || (phase === 'previewed' && tokenExpired)) &&
      archive !== null &&
      signerKeyId.trim() !== '' &&
      publicKey.trim() !== '',
  );
  let canInstall = $derived(phase === 'previewed' && accepted && !tokenExpired && preview !== null && archive !== null);
  let activeCount = $derived(revisions?.filter((row) => row.active).length ?? 0);
  let pendingCount = $derived(revisions?.filter((row) => row.pending).length ?? 0);
  let modalDirty = $derived(
    archive !== null || signerKeyId.trim() !== '' || publicKey.trim() !== '' || preview !== null,
  );
  let statusText = $derived.by(() => {
    if (busyAction === 'activate') return t('component.definitionPacks.statusActivating', parseBusyRef(busyRef));
    if (busyAction === 'rollback') return t('component.definitionPacks.statusRollingBack', parseBusyRef(busyRef));
    if (phase === 'previewing' && archive) return t('component.definitionPacks.statusPreviewing', { name: archive.name });
    if (phase === 'installing' && preview) {
      return t('component.definitionPacks.statusInstalling', { source: preview.source, revision: preview.revision });
    }
    if (phase === 'previewed' && preview) {
      return t('component.definitionPacks.statusPreviewed', { source: preview.source, revision: preview.revision });
    }
    if (phase === 'ready' && archive) return t('component.definitionPacks.statusReady', { name: archive.name });
    return t('component.definitionPacks.statusIdle');
  });

  function parseBusyRef(ref: string): { source: string; revision: string } {
    const [source = '', revision = ''] = ref.split('\u0000');
    return { source, revision };
  }

  function revisionKey(row: DefinitionPackRevision): string {
    return `${row.source}\u0000${row.revision}`;
  }

  function canActivate(row: DefinitionPackRevision): boolean {
    return row.install_state === 'installed' && row.runnable_count > 0 && !row.pending && !row.active;
  }

  function chooseArchive(event: Event) {
    const file =
      event.currentTarget instanceof HTMLInputElement ? (event.currentTarget.files?.[0] ?? null) : null;
    archive = file;
    preview = null;
    accepted = false;
    formError = null;
    phase = file ? 'ready' : 'idle';
  }

  function clearSensitive(resetFile = true) {
    signerKeyId = '';
    publicKey = '';
    preview = null;
    accepted = false;
    if (resetFile) {
      archive = null;
      if (archiveInput) archiveInput.value = '';
      phase = 'idle';
    }
  }

  function cancelForm() {
    if (phase === 'previewing' || phase === 'installing') return;
    formError = null;
    clearSensitive(true);
  }

  function openManager() {
    modalOpen = true;
    formError = null;
  }

  function closeManager() {
    formGeneration += 1;
    modalOpen = false;
    formError = null;
    clearSensitive(true);
  }

  async function load() {
    loading = true;
    error = null;
    try {
      revisions = await api.listDefinitionPacks();
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  async function previewPack() {
    if (!canPreview || !archive) return;
    const generation = formGeneration;
    phase = 'previewing';
    formError = null;
    preview = null;
    accepted = false;
    try {
      const result = await api.previewDefinitionPack({
        archive,
        signer_key_id: signerKeyId,
        public_key: publicKey,
      });
      if (generation !== formGeneration) return;
      preview = result;
      now = Date.now();
      phase = 'previewed';
    } catch (err) {
      if (generation !== formGeneration) return;
      formError = errorText(err);
      phase = 'ready';
    }
  }

  async function installPack() {
    if (!canInstall || !archive || !preview) return;
    if (tokenExpired) return;
    const generation = formGeneration;
    phase = 'installing';
    formError = null;
    try {
      await api.installDefinitionPack({
        archive,
        signer_key_id: signerKeyId,
        public_key: publicKey,
        source: preview.source,
        token: preview.token,
      });
      if (generation !== formGeneration) {
        await load();
        return;
      }
      clearSensitive(true);
      formError = null;
      await load();
    } catch (err) {
      if (generation !== formGeneration) return;
      formError = errorText(err);
      now = Date.now();
      phase = preview && !tokenExpired ? 'previewed' : 'ready';
      if (phase === 'ready') {
        preview = null;
        accepted = false;
      }
    }
  }

  async function activate(row: DefinitionPackRevision) {
    if (!canActivate(row) || busyAction) return;
    busyAction = 'activate';
    busyRef = revisionKey(row);
    formError = null;
    try {
      await api.activateDefinitionPack(row.source, row.revision);
      restartRequired = true;
      await load();
    } catch (err) {
      formError = errorText(err);
    } finally {
      busyAction = null;
      busyRef = '';
    }
  }

  async function rollback(row: DefinitionPackRevision) {
    if (!row.pending || busyAction) return;
    busyAction = 'rollback';
    busyRef = revisionKey(row);
    formError = null;
    try {
      await api.rollbackDefinitionPack(row.source, row.revision);
      await load();
    } catch (err) {
      formError = errorText(err);
    } finally {
      busyAction = null;
      busyRef = '';
    }
  }

  onMount(load);
  onDestroy(() => {
    formGeneration += 1;
    clearSensitive(true);
  });

  $effect(() => {
    if (phase !== 'previewed') return;
    const id = setInterval(() => {
      now = Date.now();
    }, 1000);
    return () => clearInterval(id);
  });
</script>

<SettingsCard
  title={t('component.definitionPacks.title')}
  description={t('component.definitionPacks.description')}>
  {#snippet action()}
    <Button variant="secondary" size="sm" onclick={openManager}>
      {t('component.definitionPacks.manage')}
    </Button>
  {/snippet}
  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if revisions === null}
    <p class="text-sm text-ink-secondary">{t('component.definitionPacks.loading')}</p>
  {:else}
    <div data-pack-summary class="flex flex-wrap items-center gap-2">
      <p class="mr-auto text-sm text-ink-secondary">
        {revisions.length === 0
          ? t('component.definitionPacks.empty')
          : t('component.definitionPacks.summary', { count: revisions.length })}
      </p>
      {#if activeCount > 0}
        <Badge tone="success">{t('component.definitionPacks.summaryActive', { count: activeCount })}</Badge>
      {/if}
      {#if pendingCount > 0}
        <Badge tone="warning">{t('component.definitionPacks.summaryPending', { count: pendingCount })}</Badge>
      {/if}
    </div>
  {/if}
</SettingsCard>

{#if modalOpen}
  <Modal
    title={t('component.definitionPacks.title')}
    width="max-w-4xl"
    dirty={modalDirty}
    onclose={closeManager}>
    <div class="flex flex-col gap-4 p-4">
  {#if error}
    <LoadError message={error} onretry={load} />
  {:else}
    {#if restartRequired}
      <Banner
        tone="warning"
        icon="warning"
        title={t('component.definitionPacks.restartRequired')}
        message={t('component.definitionPacks.restartRequiredMessage')} />
    {/if}

    <p aria-live="polite" class="text-sm text-ink-secondary">{statusText}</p>

    <div class="flex flex-col gap-4">
      <Field label={t('component.definitionPacks.archive')} for="definition-pack-archive">
        <div class="flex flex-col gap-2">
          <input
            id="definition-pack-archive"
            bind:this={archiveInput}
            class="sr-only"
            type="file"
            accept=".zip,.caravan-indexer-pack,application/zip"
            onchange={chooseArchive} />
          <Button variant="secondary" onclick={() => archiveInput?.click()}>
            {t('component.definitionPacks.chooseArchive')}
          </Button>
          {#if archive}
            <p class="text-sm text-ink-secondary">{t('component.definitionPacks.selectedFile', { name: archive.name })}</p>
          {/if}
        </div>
      </Field>

      <Field label={t('component.definitionPacks.signerKeyId')} for="definition-pack-signer">
        <TextInput
          id="definition-pack-signer"
          bind:value={signerKeyId}
          mono
          autocomplete="off"
          spellcheck={false} />
      </Field>

      <Field
        label={t('component.definitionPacks.publicKey')}
        for="definition-pack-public-key"
        help={t('component.definitionPacks.publicKeyHelp')}>
        <textarea
          id="definition-pack-public-key"
          bind:value={publicKey}
          autocomplete="off"
          spellcheck="false"
          rows="3"
          class="w-full rounded-sm border border-border-strong bg-raised px-3 py-2 font-mono text-sm text-ink
                 placeholder:text-ink-muted focus:border-accent focus:outline-none"></textarea>
      </Field>

      {#if preview && phase !== 'ready'}
        <dl class="grid gap-3 sm:grid-cols-2">
          <div>
            <dt class="micro-label">{t('component.definitionPacks.source')}</dt>
            <dd class="mt-1 font-mono text-sm break-all text-ink">{preview.source}</dd>
          </div>
          <div>
            <dt class="micro-label">{t('component.definitionPacks.revision')}</dt>
            <dd class="mt-1 font-mono text-sm break-all text-ink">{preview.revision}</dd>
          </div>
          <div>
            <dt class="micro-label">{t('component.definitionPacks.archiveDigest')}</dt>
            <dd class="mt-1 font-mono text-sm break-all text-ink">{preview.archive_digest}</dd>
          </div>
          <div>
            <dt class="micro-label">{t('component.definitionPacks.manifestDigest')}</dt>
            <dd class="mt-1 font-mono text-sm break-all text-ink">{preview.manifest_digest}</dd>
          </div>
          <div>
            <dt class="micro-label">{t('component.definitionPacks.licenseDigest')}</dt>
            <dd class="mt-1 font-mono text-sm break-all text-ink">{preview.license_digest}</dd>
          </div>
          <div>
            <dt class="micro-label">{t('component.definitionPacks.fingerprint')}</dt>
            <dd class="mt-1 font-mono text-sm break-all text-ink">{preview.signature_fingerprint}</dd>
          </div>
        </dl>

        <Field label={t('component.definitionPacks.license')}>
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <pre
            data-pack-legal
            tabindex="0"
            class="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-sm border border-border bg-raised p-3 text-sm text-ink">{preview.license}</pre>
        </Field>
        <Field label={t('component.definitionPacks.notice')}>
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <pre
            data-pack-legal
            tabindex="0"
            class="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-sm border border-border bg-raised p-3 text-sm text-ink">{preview.notice}</pre>
        </Field>

        {#if tokenExpired}
          <Banner
            tone="warning"
            icon="warning"
            title={t('component.definitionPacks.preview')}
            message={t('component.definitionPacks.tokenExpired')} />
        {:else}
          <label class="flex items-start gap-2 text-sm text-ink" for="definition-pack-accept">
            <input
              id="definition-pack-accept"
              type="checkbox"
              class="mt-1"
              checked={accepted}
              onchange={(event) => (accepted = event.currentTarget.checked)} />
            <span>{t('component.definitionPacks.acceptLicense')}</span>
          </label>
        {/if}
      {/if}

      {#if formError}
        <p class="text-sm text-danger">{formError}</p>
      {/if}

      <div class="flex flex-wrap gap-2">
        <Button
          variant="secondary"
          disabled={!canPreview || phase === 'previewing' || phase === 'installing'}
          onclick={() => void previewPack()}>
          {phase === 'previewing' ? t('component.definitionPacks.previewing') : t('component.definitionPacks.preview')}
        </Button>
        <Button
          variant="primary"
          disabled={!canInstall || phase === 'installing'}
          onclick={() => void installPack()}>
          {phase === 'installing' ? t('component.definitionPacks.installing') : t('component.definitionPacks.install')}
        </Button>
        <Button variant="ghost" disabled={phase === 'previewing' || phase === 'installing'} onclick={cancelForm}>
          {t('component.definitionPacks.cancel')}
        </Button>
      </div>
    </div>

    {#if revisions && revisions.length === 0}
      <p class="text-sm text-ink-secondary">{t('component.definitionPacks.empty')}</p>
    {:else if revisions}
      <ul class="flex flex-col gap-3">
        {#each revisions as row (revisionKey(row))}
          <li class="flex flex-col gap-3 rounded-md border border-border bg-raised px-3 py-3">
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-medium text-ink">{row.source}</span>
              <Badge mono tone="neutral">{row.revision}</Badge>
              <Badge tone="neutral">{t('component.definitionPacks.installState', { state: row.install_state })}</Badge>
              {#if row.pending}
                <Badge tone="warning">{t('component.definitionPacks.pending')}</Badge>
              {/if}
              {#if row.active}
                <Badge tone="success">{t('component.definitionPacks.active')}</Badge>
              {/if}
              {#if row.last_known_good}
                <Badge tone="info">{t('component.definitionPacks.lastKnownGood')}</Badge>
              {/if}
            </div>
            <p class="text-sm text-ink-secondary">
              {t('component.definitionPacks.counts', {
                definitions: row.definition_count,
                runnable: row.runnable_count,
              })}
            </p>
            {#if row.validation_code}
              <p class="text-sm text-ink-secondary">{t('component.definitionPacks.validation', { code: row.validation_code })}</p>
            {/if}
            <dl class="grid gap-2 sm:grid-cols-2">
              <div>
                <dt class="micro-label">{t('component.definitionPacks.licenseExpression')}</dt>
                <dd class="mt-1 text-sm text-ink">{row.license_expression}</dd>
              </div>
              <div>
                <dt class="micro-label">{t('component.definitionPacks.provenance')}</dt>
                <dd class="mt-1 text-sm break-all text-ink">{row.provenance}</dd>
              </div>
              <div>
                <dt class="micro-label">{t('component.definitionPacks.minVersion')}</dt>
                <dd class="mt-1 font-mono text-sm text-ink">{row.minimum_caravan_version}</dd>
              </div>
              <div>
                <dt class="micro-label">{t('component.definitionPacks.fingerprint')}</dt>
                <dd class="mt-1 font-mono text-sm break-all text-ink">{row.signature_fingerprint}</dd>
              </div>
              <div class="sm:col-span-2">
                <dt class="micro-label">{t('component.definitionPacks.archiveDigest')}</dt>
                <dd class="mt-1 font-mono text-sm break-all text-ink">{row.archive_digest}</dd>
              </div>
              <div class="sm:col-span-2">
                <dt class="micro-label">{t('component.definitionPacks.manifestDigest')}</dt>
                <dd class="mt-1 font-mono text-sm break-all text-ink">{row.manifest_digest}</dd>
              </div>
              <div class="sm:col-span-2">
                <dt class="micro-label">{t('component.definitionPacks.licenseDigest')}</dt>
                <dd class="mt-1 font-mono text-sm break-all text-ink">{row.license_digest}</dd>
              </div>
              {#if row.notice_digest}
                <div class="sm:col-span-2">
                  <dt class="micro-label">{t('component.definitionPacks.noticeDigest')}</dt>
                  <dd class="mt-1 font-mono text-sm break-all text-ink">{row.notice_digest}</dd>
                </div>
              {/if}
            </dl>
            <div class="flex flex-wrap gap-2">
              <Button
                variant="secondary"
                size="sm"
                disabled={!canActivate(row) || busyAction !== null}
                onclick={() => void activate(row)}>
                {t('component.definitionPacks.activate')}
              </Button>
              {#if row.pending}
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={busyAction !== null}
                  onclick={() => void rollback(row)}>
                  {t('component.definitionPacks.rollback')}
                </Button>
              {/if}
            </div>
          </li>
        {/each}
      </ul>
    {/if}
  {/if}
    </div>
  </Modal>
{/if}
