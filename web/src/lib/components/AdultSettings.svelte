<script lang="ts">
  /**
   * Settings → Adult content (PLAN phase 9 task 7a): the module's whole control
   * plane, in the order the decisions are actually made.
   *
   * 1. The master switch. Turning it on the first time creates the Adult
   *    library (hidden from DLNA); turning it off deletes nothing, so it is a
   *    visibility decision and the copy says so rather than warning about data
   *    loss that does not happen.
   * 2. Where the metadata comes from — a stash-box endpoint and its key.
   * 3. Who reaches it. Admins always do; every other account is one grant.
   * 4. Where it reaches BEYOND this screen: DLNA and prepared drives are the
   *    two surfaces that leave the browser, and the one place they are stated
   *    together is here. Neither is edited from this card — the DLNA share
   *    lives on the Playback card next to the toggle that turns DLNA on at all,
   *    and the drive rule is a flag on `caravan prepare` — so the card reports
   *    and links rather than duplicating a control.
   *
   * Everything below the master switch is unmounted while the module is off:
   * there is no roster to grant against, no provider to test, and nowhere for
   * it to reach.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import {
    SETTING_ADULT_ENABLED,
    SETTING_STASHBOX_API_KEY,
    SETTING_STASHBOX_ENDPOINT,
    STASHBOX_TPDB_ENDPOINT,
    type AdultUser,
    type Library,
    type Settings,
  } from '../api/types';
  import { session } from '../state/session.svelte';
  import { pushToast } from '../state/toast.svelte';
  import AdultEnableModal from './AdultEnableModal.svelte';
  import Badge from './Badge.svelte';
  import Banner from './Banner.svelte';
  import Button from './Button.svelte';
  import EmptyState from './EmptyState.svelte';
  import Field from './Field.svelte';
  import Icon from './Icon.svelte';
  import LoadError from './LoadError.svelte';
  import SettingsCard from './SettingsCard.svelte';
  import Skeleton from './Skeleton.svelte';
  import TextInput from './TextInput.svelte';
  import Toggle from './Toggle.svelte';

  const SELECT_CLASS =
    'h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink ' +
    'focus:border-accent focus:outline-none disabled:opacity-50';

  interface Props {
    settings: Settings;
    saving?: boolean;
    onsave: (patch: Settings, note: string) => Promise<boolean>;
  }

  let { settings, saving = false, onsave }: Props = $props();

  /**
   * The master switch, mirrored locally: it is written through
   * POST /settings/adult rather than the settings PUT, so the `settings` prop
   * this screen was handed does not update itself when it flips.
   */
  let enabled = $state(false);
  let flipping = $state(false);
  /** True while the enable setup modal is open (PLAN phase 10 task 5). */
  let enabling = $state(false);

  /** '' is stored as "use the preset", which is what the server resolves it to. */
  let preset = $state(true);
  let endpoint = $state(STASHBOX_TPDB_ENDPOINT);
  let apiKey = $state('');
  let testing = $state(false);

  let users = $state<AdultUser[] | null>(null);
  let adultLibrary = $state<Library | null>(null);
  let loading = $state(false);
  let error = $state<string | null>(null);
  /** The account whose grant is mid-flight, so only its own row goes quiet. */
  let granting = $state<number | null>(null);

  // The form is seeded from the settings it was handed exactly once. Re-seeding
  // whenever the prop changed would overwrite a half-typed key the moment any
  // other card on the screen saved.
  onMount(() => {
    const stored = (settings[SETTING_STASHBOX_ENDPOINT] ?? '').trim();
    preset = stored === '' || stored === STASHBOX_TPDB_ENDPOINT;
    endpoint = stored === '' ? STASHBOX_TPDB_ENDPOINT : stored;
    apiKey = settings[SETTING_STASHBOX_API_KEY] ?? '';
    enabled = settings[SETTING_ADULT_ENABLED] === 'true';
    void load();
  });

  /**
   * The roster and the library row both live behind routes that 404 while the
   * module is off, so they are fetched only once it is on — and again after
   * every flip, because the first enable is what creates the library.
   */
  async function load() {
    if (!enabled) {
      users = null;
      adultLibrary = null;
      error = null;
      return;
    }
    loading = true;
    try {
      const [roster, libraries] = await Promise.all([api.listAdultUsers(), api.listLibraries()]);
      users = roster;
      adultLibrary = libraries.find((l) => l.kind === 'adult') ?? null;
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  /**
   * The two directions are not symmetrical (PLAN phase 10 task 5).
   *
   * Turning it ON needs a stash-box credential that works and a statement of
   * what it exposes, so the switch opens the setup modal and the write happens
   * there. Turning it OFF needs neither — it deletes nothing and exposes
   * nothing — so it goes straight through, and a disable never prompts.
   */
  function flip(next: boolean) {
    if (next) {
      enabling = true;
      return;
    }
    void disable();
  }

  async function disable() {
    flipping = true;
    try {
      await api.setAdultEnabled(false);
      enabled = false;
      // The nav item, the discover shelves and the request form all read
      // `session.adult`, which the server recomputes on /auth/me.
      await session.refresh();
      pushToast('Adult content is off. Nothing was deleted.', 'neutral');
      await load();
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      flipping = false;
    }
  }

  /**
   * The modal already wrote the credential it proved, as part of the same
   * atomic enable — so this only catches up the screen with what is now true.
   */
  async function onEnabled(committed: { endpoint: string; apiKey: string }) {
    enabling = false;
    enabled = true;
    // Re-seed the source card from what the enable committed, so the fields
    // below show the credential that is actually in force rather than the
    // blanks this screen mounted with.
    preset = committed.endpoint === '';
    endpoint = committed.endpoint === '' ? STASHBOX_TPDB_ENDPOINT : committed.endpoint;
    apiKey = committed.apiKey;
    await session.refresh();
    pushToast('Adult content is on.', 'success');
    await load();
  }

  async function saveSource() {
    await onsave(
      {
        // The preset is stored as the empty string rather than the literal URL:
        // it is what the server already resolves a blank endpoint to, so a
        // future change of default follows the server instead of being frozen
        // into everyone's settings table.
        [SETTING_STASHBOX_ENDPOINT]: preset ? '' : endpoint.trim(),
        [SETTING_STASHBOX_API_KEY]: apiKey.trim(),
      },
      'Metadata source saved.',
    );
  }

  /**
   * The connection test is a real site search: it is the same call the Add site
   * picker makes, so it exercises the endpoint, the credential and the parsing
   * together. Zero results still count as a pass — an answer is the thing being
   * tested.
   */
  async function test() {
    testing = true;
    try {
      await api.searchSites('test');
      pushToast('The metadata source answered.', 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      testing = false;
    }
  }

  async function grant(user: AdultUser, granted: boolean) {
    granting = user.id;
    try {
      const updated = await api.setAdultAccess(user.id, granted);
      users = (users ?? []).map((u) => (u.id === updated.id ? updated : u));
      pushToast(
        granted ? `${updated.username} can see adult content.` : `${updated.username} can no longer see adult content.`,
        granted ? 'success' : 'neutral',
      );
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      granting = null;
    }
  }

  let canSaveSource = $derived(!saving && (preset || endpoint.trim() !== ''));
  let sharedOnDlna = $derived(adultLibrary?.dlna_visible === true);
</script>

<div class="flex flex-col gap-5">
  <SettingsCard
    title="Adult content"
    description="An adult library with its own metadata source, its own indexer categories and its own access list. Off by default, and invisible to everyone while it is off.">
    {#snippet action()}
      <Badge tone={enabled ? 'accent' : 'neutral'}>{enabled ? 'On' : 'Off'}</Badge>
    {/snippet}

    <Toggle
      checked={enabled}
      disabled={flipping || enabling}
      label="Enable adult content"
      onchange={flip} />

    <p class="text-sm text-ink-secondary">
      Turning this off hides the module everywhere — the sidebar, Discover, requests, the calendar,
      search and the DLNA tree. It deletes nothing: the sites, the scenes and the files stay exactly
      where they are, and turning it back on finds them.
    </p>
  </SettingsCard>

  {#if enabled}
    {#if error}
      <LoadError message={error} onretry={load} />
    {/if}

    <SettingsCard
      title="Metadata source"
      description="A stash-box provider supplies sites, scenes, performers and artwork — TMDB does none of this.">
      <Field
        label="Endpoint"
        for="stashbox-endpoint"
        help="The provider's GraphQL endpoint. The preset follows Caravan's default; pick Custom to point at your own stash-box.">
        <select
          id="stashbox-endpoint"
          value={preset ? 'tpdb' : 'custom'}
          onchange={(event) => (preset = event.currentTarget.value === 'tpdb')}
          class={SELECT_CLASS}>
          <option value="tpdb">ThePornDB — {STASHBOX_TPDB_ENDPOINT}</option>
          <option value="custom">Custom stash-box…</option>
        </select>
      </Field>

      {#if !preset}
        <Field
          label="Custom endpoint"
          for="stashbox-endpoint-url"
          help="An absolute http(s) URL. Anything else is refused rather than silently ignored.">
          <TextInput
            id="stashbox-endpoint-url"
            bind:value={endpoint}
            mono
            placeholder={STASHBOX_TPDB_ENDPOINT} />
        </Field>
      {/if}

      <Field
        label="API key"
        for="stashbox-api-key"
        help="Stored in the database, never in caravan.yaml or logs.">
        <TextInput id="stashbox-api-key" bind:value={apiKey} type="password" mono placeholder="•••••" />
      </Field>

      <div class="flex flex-wrap items-center gap-2">
        <Button variant="primary" disabled={!canSaveSource} onclick={saveSource}>
          <Icon name="check" size={14} />
          {saving ? 'Saving…' : 'Save'}
        </Button>
        <Button variant="ghost" disabled={testing} onclick={test}>
          {testing ? 'Testing…' : 'Test'}
        </Button>
      </div>
    </SettingsCard>

    <SettingsCard
      title="Member access"
      description="Adult content is hidden from every account that is not on this list. Admins always reach it.">
      {#if loading && users === null}
        <div class="flex flex-col gap-2">
          {#each Array.from({ length: 2 }) as _, i (i)}
            <Skeleton class="h-12 w-full rounded-md" />
          {/each}
        </div>
      {:else if (users ?? []).length === 0}
        <EmptyState
          icon="inbox"
          title="No accounts yet"
          message="This Caravan is open, so anyone who can reach it is an admin and already sees adult content. Add accounts under Settings → Users to decide who does." />
      {:else}
        <ul class="flex flex-col gap-2">
          {#each users ?? [] as user (user.id)}
            <li class="flex flex-wrap items-center gap-3 rounded-md border border-border bg-surface px-3 py-3">
              <div class="min-w-0 flex-1">
                <p class="flex flex-wrap items-center gap-2">
                  <span class="truncate text-base font-medium text-ink">{user.username}</span>
                  <Badge mono tone={user.role === 'admin' ? 'accent' : 'neutral'}>
                    {user.role === 'admin' ? 'ADMIN' : 'MEMBER'}
                  </Badge>
                </p>
              </div>
              <div class="flex shrink-0 items-center gap-2">
                {#if user.always_granted}
                  <span class="text-sm text-ink-secondary">Always has access</span>
                {:else}
                  <Toggle
                    checked={user.granted}
                    disabled={granting === user.id}
                    labelHidden
                    label="Adult content for {user.username}"
                    onchange={(next) => grant(user, next)} />
                {/if}
              </div>
            </li>
          {/each}
        </ul>
      {/if}
    </SettingsCard>

    <SettingsCard
      title="Where it reaches"
      description="The two ways library content leaves this browser. Both keep adult content out unless you say otherwise.">
      <dl class="flex flex-col gap-3">
        <div class="flex flex-wrap items-start justify-between gap-3 rounded-md border border-border bg-surface px-3 py-3">
          <div class="min-w-0 flex-1">
            <dt class="text-base font-medium text-ink">DLNA</dt>
            <dd class="text-sm text-ink-secondary">
              Televisions and apps on this network. Sharing the Adult library is its own toggle on
              the Playback screen — DLNA has no accounts, so anything shared there is readable by
              every device on the network.
            </dd>
          </div>
          <div class="flex shrink-0 items-center gap-2">
            <Badge tone={sharedOnDlna ? 'warning' : 'neutral'}>
              {sharedOnDlna ? 'Shared on this network' : 'Not shared'}
            </Badge>
            <a href="/settings/playback" class="text-sm text-accent-text hover:underline">Playback</a>
          </div>
        </div>

        <div class="flex flex-wrap items-start justify-between gap-3 rounded-md border border-border bg-surface px-3 py-3">
          <div class="min-w-0 flex-1">
            <dt class="text-base font-medium text-ink">Portable drives</dt>
            <dd class="text-sm text-ink-secondary">
              <code class="font-mono text-sm">caravan prepare</code> leaves the Adult library out of
              a prepared drive. Passing <code class="font-mono text-sm">--include-adult</code> is the
              only way it goes along.
            </dd>
          </div>
          <div class="flex shrink-0 items-center">
            <Badge tone="neutral">Excluded</Badge>
          </div>
        </div>
      </dl>

      {#if sharedOnDlna}
        <Banner
          tone="warning"
          icon="warning"
          title="The Adult library is on the network"
          message="DLNA has no accounts — every device on this network can browse anything shared here. Turn it off on the Playback screen if that is not what you meant." />
      {/if}
    </SettingsCard>
  {/if}
</div>

{#if enabling}
  <!-- Cancel closes it and nothing was written: the enable is one server-side
       call that validates before it writes, so an abandoned setup leaves the
       endpoint, the key and the switch exactly where they were. -->
  <AdultEnableModal
    initialEndpoint={preset ? '' : endpoint}
    initialApiKey={apiKey}
    onclose={() => (enabling = false)}
    onenabled={onEnabled} />
{/if}
