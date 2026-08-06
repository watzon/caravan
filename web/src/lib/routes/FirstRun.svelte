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
  let root = $state(system.status?.storage_root ?? '');
  let adminUsername = $state('');
  let adminPassword = $state('');
  let adminConfirm = $state('');
  let accountReady = $state(false);
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
      testResult = { ok: false, message: 'Enter a TMDB API key to test.' };
      return;
    }
    testing = true;
    try {
      // The unsaved key travels in the body: proving it before it is stored is
      // the whole point of testing it here.
      await api.testMetadataKey(trimmedKey);
      proven = trimmedKey;
      testResult = { ok: true, message: 'Key works — TMDB answered.' };
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
      accountError = 'Enter a username for the administrator account.';
      return;
    }
    if (adminPassword.length < 8) {
      accountError = 'Use a password with at least 8 characters.';
      return;
    }
    if (adminPassword !== adminConfirm) {
      accountError = 'The passwords do not match.';
      return;
    }
    try {
      await api.setupAdmin(adminUsername.trim(), adminPassword);
      await session.refresh();
      accountReady = true;
      accountError = null;
    } catch (err) {
      accountError = errorText(err);
    }
  }

  async function save() {
    const value = root.trim();
    if (value === '') {
      error = 'Enter the folder Caravan should treat as its storage root.';
      return;
    }
    if (!accountReady) {
      error = 'Create the administrator account before finishing setup.';
      return;
    }
    if (trimmedKey === '' && !skipped) {
      error = 'Enter a TMDB API key, or skip that step on purpose.';
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
          pushToast('Scan started in the background.', 'success');
        } catch (err) {
          pushToast(
            `Storage root saved, but Caravan could not start the scan: ${errorText(err)}`,
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
        Let’s load the caravan
      </h1>
      <p class="max-w-md text-base text-ink-secondary">
        Four decisions and you’re done. Everything else ships with sensible defaults and can be
        changed later in Settings.
      </p>
    </header>

    <form
      class="flex flex-col gap-4"
      onsubmit={(event) => {
        event.preventDefault();
        save();
      }}>
      <section class="flex flex-col gap-4 rounded-lg border border-border bg-surface p-5">
        {@render step(1, 'Create your administrator account')}
        {#if accountReady}
          <p class="flex items-center gap-2 text-sm text-success">
            <span class="size-2 shrink-0 rounded-full bg-success"></span>
            Administrator account created. Keep these credentials safe.
          </p>
        {:else}
          <Field label="Username" for="admin-username" error={accountError}>
            <TextInput id="admin-username" bind:value={adminUsername} autocomplete="username" />
          </Field>
          <div class="grid gap-4 sm:grid-cols-2">
            <Field label="Password" for="admin-password" help="At least 8 characters.">
              <TextInput
                id="admin-password"
                bind:value={adminPassword}
                type="password"
                autocomplete="new-password" />
            </Field>
            <Field label="Confirm password" for="admin-confirm">
              <TextInput
                id="admin-confirm"
                bind:value={adminConfirm}
                type="password"
                autocomplete="new-password" />
            </Field>
          </div>
          <Button variant="secondary" type="button" onclick={createAccount}>Create account</Button>
        {/if}
      </section>

      <section class="flex flex-col gap-4 rounded-lg border border-border bg-surface p-5">
        {@render step(2, 'Where does your media live?')}
        <Field
          label="Storage root"
          for="storage-root"
          help="Absolute path on the machine running Caravan — /data in Docker, the drive root on a portable disk. Downloads, the library and everything in between stay under this one root, so imports are instant hardlinks rather than copies.">
          <TextInput id="storage-root" bind:value={root} mono autofocus placeholder="/data" />
        </Field>
      </section>

      <section class="flex flex-col gap-4 rounded-lg border border-border bg-surface p-5">
        {@render step(3, 'How should Caravan identify it?')}
        <Field
          label="TMDB API key"
          for="tmdb-key"
          error={testResult && !testResult.ok ? testResult.message : null}
          help="A free TMDB API key names every movie and show Caravan touches. It is stored in the database, never in caravan.yaml or logs.">
          <div class="flex items-start gap-2">
            <TextInput
              id="tmdb-key"
              bind:value={tmdbKey}
              oninput={onKeyInput}
              type="password"
              mono
              placeholder="•••••" />
            <Button variant="secondary" disabled={testing} onclick={test} class="shrink-0">
              {testing ? 'Testing…' : 'Test'}
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
            Skipped. The library still scans and imports, but nothing is matched and no posters
            arrive until a key lands in Settings → Metadata.
          </p>
        {:else}
          <Button variant="ghost" onclick={skip} class="self-start">Skip for now</Button>
        {/if}
      </section>

      <section class="flex flex-col gap-3 rounded-lg border border-border bg-surface p-5">
        <div class="flex items-start justify-between gap-4">
          {@render step(4, 'Already have a library?')}
          <Toggle
            checked={scanNow}
            labelHidden
            label="Scan for existing media now"
            onchange={(next) => (scanNow = next)} />
        </div>
        <p class="text-base text-ink-secondary">
          Scan the storage root now. Caravan matches what it finds, renames into clean Jellyfin
          folders, and parks anything it cannot identify for your review — nothing is ever deleted.
        </p>
      </section>

      {#if error}
        <LoadError message={error} />
      {/if}

      <Button variant="primary" type="submit" disabled={saving}>
        <Icon name="check" size={14} />
        {saving ? 'Starting…' : 'Start Caravan'}
      </Button>

      <p class="text-center text-sm text-ink-muted">
        Finish setup in <a href="/settings/indexers" class="text-accent-text hover:underline"
          >Indexers</a
        >, <a href="/settings/downloads" class="text-accent-text hover:underline">Downloads</a>,
        or <a href="/settings/quality-profiles" class="text-accent-text hover:underline"
          >Download profiles</a
        >.
      </p>
    </form>
  </div>
</div>
