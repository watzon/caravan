<script lang="ts">
  /**
   * First run (SPEC §10.1, PLAN phase 10 task 1): three light steps on one
   * screen — where the media lives, how Caravan identifies it, and whether to
   * scan what is already there. Everything else ships with defaults.
   *
   * The metadata step is new, and it is here rather than in Settings for one
   * reason: a first run that ends without a TMDB key ends in a Caravan where
   * search, Discover and every add are degraded, and the person who could have
   * fixed that in ten seconds has already left the screen. So the key is asked
   * for where it is needed, proved where it is typed, and skipping it is an
   * explicit choice that names its own consequence.
   *
   * Nothing is written until Start Caravan, and the key is proved BEFORE the
   * first write: a wrong key never reaches the database, and a failed test
   * leaves the user on this screen with the field they mistyped still in front
   * of them.
   *
   * There are deliberately zero adult-content references here (PLAN phase 10
   * task 6). The module is invisible while it is off, and a first run is the
   * one place that promise is most visible.
   */
  import { api, errorText } from '../api/client';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import TextInput from '../components/TextInput.svelte';
  import Toggle from '../components/Toggle.svelte';
  import { SETTING_TMDB_API_KEY } from '../api/types';
  import { navigate } from '../router.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { system } from '../state/system.svelte';

  import { session } from '../state/session.svelte';
  import { useI18n } from '../i18n.svelte';

  const { t } = useI18n();

  let root = $state(system.status?.storage_root ?? '');
  let adminUsername = $state('');
  let adminPassword = $state('');
  let adminConfirm = $state('');
  let accountCreated = $state(false);
  let accountReady = $derived(accountCreated || system.status?.password_set === true);
  let accountError = $state<string | null>(null);
  let tmdbKey = $state('');
  let scanNow = $state(true);
  let saving = $state(false);
  let error = $state<string | null>(null);

  /** The Test button's verdict, in the indexer card's inline ✓/✕ idiom. */
  let testing = $state(false);
  let testResult = $state<{ ok: boolean; message: string } | null>(null);
  /**
   * The exact key string TMDB has accepted. Comparing against the value rather
   * than holding a boolean is what makes "tested, then edited" read as untested
   * again — and it is the same thing the server caches its verdict against, so
   * submitting a proven key costs no second round trip.
   */
  let proven = $state<string | null>(null);

  /**
   * The escape hatch, taken on purpose. It is a state rather than just an empty
   * field because "I have not typed it yet" and "I am going without one" are
   * different answers, and only the second one should let Start Caravan through
   * without a test.
   */
  let skipped = $state(false);

  let trimmedKey = $derived(tmdbKey.trim());
  let keyProven = $derived(trimmedKey !== '' && trimmedKey === proven);

  function onKeyInput() {
    // Typing is un-skipping, and it invalidates a verdict about a different
    // string. Both are the same thought: this is a new answer.
    skipped = false;
    testResult = null;
  }

  async function test() {
    if (trimmedKey === '') {
      testResult = { ok: false, message: t('route.firstRun.keyRequired') };
      return;
    }
    testing = true;
    try {
      // The unsaved key travels in the body: proving it before it is stored is
      // the whole point of testing it here.
      await api.testMetadataKey(trimmedKey);
      proven = trimmedKey;
      testResult = { ok: true, message: t('route.firstRun.keyWorks') };
    } catch (err) {
      proven = null;
      testResult = { ok: false, message: errorText(err) };
    } finally {
      testing = false;
    }
  }

  function skip() {
    skipped = true;
    tmdbKey = '';
    proven = null;
    testResult = null;
  }
  async function createAccount() {
    accountError = null;
    if (adminUsername.trim() === '') {
      accountError = t('route.firstRun.usernameRequired');
      return;
    }
    if (adminPassword.length < 8) {
      accountError = t('route.firstRun.passwordLength');
      return;
    }
    if (adminPassword !== adminConfirm) {
      accountError = t('route.firstRun.passwordMismatch');
      return;
    }
    try {
      await api.setupAdmin(adminUsername.trim(), adminPassword);
      await session.refresh();
      accountCreated = true;
      accountError = null;
    } catch (err) {
      accountError = errorText(err);
    }
  }

  async function save() {
    const value = root.trim();
    if (value === '') {
      error = t('route.firstRun.rootRequired');
      return;
    }
    if (!accountReady) {
      error = t('route.firstRun.accountRequired');
      return;
    }
    if (trimmedKey === '' && !skipped) {
      error = t('route.firstRun.keyOrSkip');
      return;
    }

    saving = true;
    error = null;
    try {
      // Proved before anything is written. A key that fails here has cost the
      // user one round trip and nothing else — no half-configured install, no
      // credential on disk that is known to be wrong.
      if (trimmedKey !== '' && !keyProven) {
        await test();
        if (!keyProven) {
          saving = false;
          return;
        }
      }

      // The repoint endpoint rather than a plain settings write: the storage
      // root is the one setting with rules attached — absolute, existing, a
      // folder — and typing a path that does not exist is exactly the mistake
      // a first run makes. PUT /settings enforces none of them.
      await api.repointStorageRoot(value);

      if (trimmedKey !== '') {
        // Instant: the server already holds a verdict for this exact string,
        // so the save does not pay for a second live check.
        await api.putSettings({ [SETTING_TMDB_API_KEY]: trimmedKey });
      }

      if (scanNow) {
        // Starting is a quick, detached request. A large library must not hold
        // first run open while the scan walks the storage root.
        try {
          await api.rescan();
          pushToast(t('route.firstRun.scanStarted'), 'success');
        } catch (err) {
          pushToast(
            t('route.firstRun.scanFailed', { error: errorText(err) }),
            'warning',
          );
        }
      }

      // Last, not first: refreshing publishes "setup is done", and the route
      // gate in App.svelte acts on that the moment it lands.
      await system.refresh();
      navigate('/settings');
    } catch (err) {
      error = errorText(err);
    } finally {
      saving = false;
    }
  }
</script>

{#snippet step(number: number, heading: string)}
  <div class="flex items-center gap-3">
    <span
      class="flex size-5 shrink-0 items-center justify-center rounded-full bg-accent font-mono text-xs font-semibold text-ink-inverse"
      aria-hidden="true">
      {number}
    </span>
    <h2 class="font-display text-md font-semibold tracking-tight text-ink">{heading}</h2>
  </div>
{/snippet}

<div class="flex min-h-full items-center justify-center bg-bg p-6">
  <div class="flex w-full max-w-xl flex-col gap-6 py-8">
    <header class="flex flex-col items-center gap-3 text-center">
      <span class="text-accent" aria-hidden="true">
        <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M3 17h18" />
          <path d="M5 17V9l4-4h7a3 3 0 0 1 3 3v9" />
          <circle cx="8" cy="19" r="2" />
          <circle cx="17" cy="19" r="2" />
        </svg>
      </span>
      <h1 class="font-display text-2xl font-bold tracking-tight text-ink">
        {t('route.firstRun.title')}
      </h1>
      <p class="max-w-md text-base text-ink-secondary">
        {t('route.firstRun.intro')}
      </p>
    </header>

    <form
      class="flex flex-col gap-4"
      onsubmit={(event) => {
        event.preventDefault();
        save();
      }}>
      <section class="flex flex-col gap-4 rounded-lg border border-border bg-surface p-5">
        {@render step(1, t('route.firstRun.accountStep'))}
        {#if accountReady}
          <p class="flex items-center gap-2 text-sm text-success">
            <span class="size-2 shrink-0 rounded-full bg-success"></span>
            {t('route.firstRun.accountCreated')}
          </p>
        {:else}
          <Field label={t('route.firstRun.username')} for="admin-username" error={accountError}>
            <TextInput id="admin-username" bind:value={adminUsername} autocomplete="username" />
          </Field>
          <div class="grid gap-4 sm:grid-cols-2">
            <Field label={t('route.firstRun.password')} for="admin-password" help={t('route.firstRun.passwordHelp')}>
              <TextInput
                id="admin-password"
                bind:value={adminPassword}
                type="password"
                autocomplete="new-password" />
            </Field>
            <Field label={t('route.firstRun.confirmPassword')} for="admin-confirm">
              <TextInput
                id="admin-confirm"
                bind:value={adminConfirm}
                type="password"
                autocomplete="new-password" />
            </Field>
          </div>
          <Button variant="secondary" type="button" onclick={createAccount}>{t('route.firstRun.createAccount')}</Button>
        {/if}
      </section>

      <section class="flex flex-col gap-4 rounded-lg border border-border bg-surface p-5">
        {@render step(2, t('route.firstRun.storageStep'))}
        <Field
          label={t('route.firstRun.storageRoot')}
          for="storage-root"
          help={t('route.firstRun.storageHelp')}>
          <TextInput id="storage-root" bind:value={root} mono autofocus placeholder="/data" />
        </Field>
      </section>

      <section class="flex flex-col gap-4 rounded-lg border border-border bg-surface p-5">
        {@render step(3, t('route.firstRun.identifyStep'))}
        <Field
          label={t('route.firstRun.tmdbKey')}
          for="tmdb-key"
          error={testResult && !testResult.ok ? testResult.message : null}
          help={t('route.firstRun.tmdbHelp')}>
          <div class="flex flex-col items-stretch gap-2 sm:flex-row sm:items-start">
            <TextInput
              id="tmdb-key"
              bind:value={tmdbKey}
              oninput={onKeyInput}
              type="password"
              mono
              placeholder="•••••"
              class="min-w-0 flex-1" />
            <Button variant="secondary" disabled={testing} onclick={test} class="shrink-0">
              {testing ? t('route.firstRun.testing') : t('route.firstRun.test')}
            </Button>
          </div>
        </Field>

        {#if testResult?.ok}
          <p class="-mt-2 flex items-center gap-2 text-sm text-success">
            <span class="size-2 shrink-0 rounded-full bg-success"></span>
            {testResult.message}
          </p>
        {/if}

        <!-- The escape hatch names what it costs. It is a button rather than a
             quiet link because going without a key is a real choice, and one
             that should be made rather than fallen into. -->
        {#if skipped}
          <p class="text-sm text-warning">
            {t('route.firstRun.skipped')}
          </p>
        {:else}
          <Button variant="ghost" onclick={skip} class="self-start">{t('route.firstRun.skip')}</Button>
        {/if}
      </section>

      <section class="flex flex-col gap-3 rounded-lg border border-border bg-surface p-5">
        <div class="flex items-start justify-between gap-4">
          {@render step(4, t('route.firstRun.libraryStep'))}
          <Toggle
            checked={scanNow}
            labelHidden
            label={t('route.firstRun.scanLabel')}
            onchange={(next) => (scanNow = next)} />
        </div>
        <p class="text-base text-ink-secondary">
          {t('route.firstRun.scanDescription')}
        </p>
      </section>

      {#if error}
        <LoadError message={error} />
      {/if}

      <Button variant="primary" type="submit" disabled={saving}>
        <Icon name="check" size={14} />
        {saving ? t('route.firstRun.starting') : t('route.firstRun.start')}
      </Button>

      <p class="text-center text-sm text-ink-muted">
        {t('route.firstRun.finishSetupPrefix')}<a href="/settings/indexers" class="text-accent-text hover:underline"
          >{t('route.firstRun.indexers')}</a
        >, <a href="/settings/downloads" class="text-accent-text hover:underline">{t('route.firstRun.downloads')}</a>,
        or <a href="/settings/quality-profiles" class="text-accent-text hover:underline"
          >{t('route.firstRun.profiles')}</a
        >.
      </p>
    </form>
  </div>
</div>
