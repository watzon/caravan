<script lang="ts">
  /**
   * DESIGN.md §5: fixed 240px sidebar on --color-surface with a hairline right
   * border. Nav is phase-gated: Wanted and Calendar arrived with phase 3 and
   * Convert with phase 4, so every entry here has a screen behind it.
   *
   * The persistent bottom slot holds system status (disk free, engine health).
   *
   * A member sees the Explore group and nothing else: the other three lead to
   * screens the server answers 403 for (SPEC §11), and the status card below
   * reads a status they may not fetch. The one exception is the Adult shelf,
   * which a granted member may read — so their Library group holds that row
   * alone rather than being suppressed wholesale.
   */
  import { onMount } from 'svelte';
  import { isActive } from '../router.svelte';
  import {
    SETTINGS_CATALOG,
    SETTINGS_CATEGORIES,
    settingsCategoryLabel,
    settingsEntryForSection,
    settingsHref,
    settingsLabel,
  } from '../settings/catalog';
  import { auth } from '../state/auth.svelte';
  import { session } from '../state/session.svelte';
  import { BADGE_POLL_MS, downloads } from '../state/downloads.svelte';
  import { REQUESTS_BADGE_POLL_MS, requests } from '../state/requests.svelte';
  import { system } from '../state/system.svelte';
  import { providers } from '../state/providers.svelte';
  import { formatBytes } from '../format';
  import { metadataStateLabel, PROVIDER_TMDB } from '../credentials';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import Icon, { type IconName } from '../components/Icon.svelte';
  import ProgressBar from '../components/ProgressBar.svelte';
  import SafeShutdown from '../components/SafeShutdown.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import type { Tone } from '../status';
  import { TONE_DOT } from '../status';
  import { useI18n, type TranslationKey } from '../i18n.svelte';

  interface Props {
    open: boolean;
    onclose: () => void;
    /** Defined only for `/settings` routes; '' names the overview. */
    settingsSection?: string;
  }

  let { open, onclose, settingsSection = undefined }: Props = $props();

  const { t, tp } = useI18n();

  let narrow = $state(false);
  let closeButton = $state<HTMLButtonElement | undefined>(undefined);

  onMount(() => {
    // jsdom and SSR-adjacent hosts may expose `window` without matchMedia. In
    // that case CSS still owns the layout; leave the drawer in its desktop
    // state rather than making mounting the shell depend on a browser-only API.
    if (typeof window.matchMedia !== 'function') return;

    const media = window.matchMedia('(max-width: 767px)');
    const sync = () => (narrow = media.matches);
    sync();
    media.addEventListener('change', sync);
    return () => media.removeEventListener('change', sync);
  });

  function closeForNavigation() {
    if (narrow) onclose();
  }

  $effect(() => {
    if (narrow && open) closeButton?.focus();
  });

  interface NavItem {
    href: string;
    label: TranslationKey;
    icon: IconName;
    /** Extra paths that also light this row up — Discover also owns `/`. */
    alsoActiveOn?: string[];
    /**
     * The row belongs to the adult module: it appears only while the module is
     * visible to this account, and it is then the ONE Library row a granted
     * member gets (the rest of that group is an admin's).
     */
    adult?: true;
  }

  const EXPLORE: NavItem[] = [
    { href: '/discover', label: 'app.title.discover', icon: 'compass', alsoActiveOn: ['/'] },
    { href: '/requests', label: 'app.title.requests', icon: 'inbox' },
  ];

  const LIBRARY: NavItem[] = [
    { href: '/movies', label: 'app.title.movies', icon: 'film' },
    { href: '/series', label: 'app.title.series', icon: 'tv' },
    // Between Series and Wanted (the Paper design). It is a shelf, so it sits
    // with the shelves rather than in Explore, even for the granted member
    // whose Library group holds nothing else.
    { href: '/adult', label: 'app.title.adult', icon: 'flame', adult: true },
    // Not the search icon any more: that belongs to the Activity group's
    // Search row, and two nav rows wearing one glyph is two rows you cannot
    // tell apart at a glance. A bookmark is what Wanted is: a list of titles
    // set aside to be found later.
    { href: '/wanted', label: 'app.title.wanted', icon: 'bookmark' },
    { href: '/calendar', label: 'app.title.calendar', icon: 'inbox' },
  ];

  const ACTIVITY: NavItem[] = [
    // First: it is where a download starts. Queue, Convert and History are all
    // about a download that already exists.
    { href: '/search', label: 'app.title.search', icon: 'search' },
    { href: '/queue', label: 'app.title.queue', icon: 'download' },
    { href: '/convert', label: 'app.title.convert', icon: 'refresh' },
    { href: '/history', label: 'app.title.history', icon: 'pulse' },
  ];

  const MANAGE: NavItem[] = [
    { href: '/scan-review', label: 'app.title.scanReview', icon: 'inbox' },
    { href: '/settings', label: 'app.title.settings', icon: 'settings' },
  ];

  const SETTINGS_GROUPS = SETTINGS_CATEGORIES.map((category) => ({
    category,
    items: SETTINGS_CATALOG.filter((entry) => entry.category === category),
  }));

  // The badge is the only reason the shell polls downloads, so it does so
  // lazily; the queue screen subscribes at its own faster rate while open.
  // A member has no queue badge and no permission to ask for one.
  $effect(() => (session.isAdmin ? downloads.subscribe(BADGE_POLL_MS) : undefined));

  // Same deal for pending requests: the badge is work waiting on the user, so
  // it stays live, at the laziest rate that still feels current.
  $effect(() => requests.subscribe(REQUESTS_BADGE_POLL_MS));

  // The nav counts come from system status, which is otherwise only fetched
  // on mount and after setting changes. A lazy poll keeps them honest without
  // making the shell chatty.
  const COUNT_POLL_MS = 60_000;
  $effect(() => {
    if (!session.isAdmin) return;
    const timer = setInterval(() => void system.refresh(), COUNT_POLL_MS);
    return () => clearInterval(timer);
  });

  let status = $derived(system.status);

  /**
   * The Library group's rows for whoever is reading.
   *
   * An adult row needs the module to be visible to this account; every other
   * row needs the admin role. That is why the filter is per row rather than per
   * group: a granted member's Library group holds the Adult shelf and nothing
   * else, and an admin who switched the module off gets the group they had
   * before it existed.
   */
  let libraryItems = $derived(
    LIBRARY.filter((item) => (item.adult ? session.adult : session.isAdmin)),
  );

  /**
   * What each nav item counts, and how loudly (DESIGN.md §5/§6).
   *
   * Neutral is inventory, information rather than a summons. Accent is work in
   * flight, warning is a backlog waiting on the user. Zeros render nothing: a
   * row of grey 0s is noise.
   */
  type NavBadge = { count: number; tone: Tone; title: string };
  function navBadge(href: string): NavBadge | null {
    const counts = status?.counts;
    switch (href) {
      case '/requests': {
        const count = requests.pendingCount;
        return count
          ? { count, tone: 'warning', title: tp('sidebar.badge.pendingRequest', count) }
          : null;
      }
      case '/movies': {
        const count = counts?.movies ?? 0;
        return count
          ? { count, tone: 'neutral', title: tp('sidebar.badge.movie', count) }
          : null;
      }
      case '/series': {
        const count = counts?.series ?? 0;
        return count
          ? { count, tone: 'neutral', title: tp('sidebar.badge.series', count) }
          : null;
      }
      case '/adult': {
        const count = counts?.sites ?? 0;
        return count
          ? { count, tone: 'neutral', title: tp('sidebar.badge.adultSite', count) }
          : null;
      }
      case '/wanted': {
        const count = counts?.wanted ?? 0;
        return count
          ? { count, tone: 'warning', title: t('sidebar.badge.wanted', { count }) }
          : null;
      }
      case '/queue': {
        const count = downloads.activeCount;
        return count
          ? { count, tone: 'accent', title: tp('sidebar.badge.download', count) }
          : null;
      }
      case '/convert': {
        const count = counts?.converting ?? 0;
        return count
          ? { count, tone: 'neutral', title: tp('sidebar.badge.conversion', count) }
          : null;
      }
      case '/scan-review': {
        const count = counts?.unmatched ?? 0;
        return count
          ? { count, tone: 'warning', title: tp('sidebar.badge.unmatchedFile', count) }
          : null;
      }
      default:
        return null;
    }
  }

  let usedFraction = $derived.by(() => {
    const s = status;
    if (!s || s.disk_total_bytes <= 0) return 0;
    return (s.disk_total_bytes - s.disk_free_bytes) / s.disk_total_bytes;
  });

  let diskUsage = $derived.by(() => {
    const s = status;
    if (!s || s.disk_total_bytes <= 0) return null;
    return {
      used: Math.max(0, s.disk_total_bytes - s.disk_free_bytes),
      free: s.disk_free_bytes,
    };
  });

  let health = $derived.by((): { tone: Tone; label: string } => {
    if (system.error) return { tone: 'danger', label: t('sidebar.health.serverUnreachable') };
    const s = status;
    if (!s) return { tone: 'neutral', label: t('sidebar.health.checking') };
    if (s.dirty) return { tone: 'danger', label: t('sidebar.health.dirtyShutdown') };
    if (s.engine_health === 'ok') return { tone: 'success', label: t('sidebar.health.ready') };
    if (s.engine_health === 'degraded') return { tone: 'warning', label: t('sidebar.health.engineDegraded') };
    if (s.engine_health === 'error') return { tone: 'danger', label: t('sidebar.health.engineError') };
    // "unconfigured": no storage root yet, so no engine. This is a setup state.
    return { tone: 'neutral', label: t('sidebar.health.notSetUp') };
  });

  /**
   * The provider display name to warn about a credential under.
   *
   * `providers` is an admin-only list loaded lazily by the surfaces that need
   * names, so the shell usually has none and `name()` hands back the raw id —
   * a worse label but never a wrong one. TMDB is the exception: its row
   * predates the list entirely and must read the same either way.
   */
  function credentialName(id: string): string {
    const named = providers.name(id);
    if (named !== id) return named;
    return id === PROVIDER_TMDB ? 'TMDB' : id;
  }

  /**
   * The credentials that need attention (PLAN phase 10 task 2), one row each.
   *
   * The card is quiet while a key works — a healthy credential is not news —
   * and loud in exactly the two states where the surfaces behind that provider
   * are degraded. Every state is read from the status payload's cached
   * verdicts, so these rows cost no upstream call however often it refreshes.
   */
  let credentialRows = $derived(
    system.unhealthyCredentials
      .map((c) => ({
        id: c.id,
        reason: c.reason,
        label: metadataStateLabel(c.state, credentialName(c.id)),
      }))
      .filter((row): row is typeof row & { label: string } => row.label !== null),
  );
  let signOutLabel = $derived(
    auth.busy
      ? t('sidebar.signOut.busy')
      : t('sidebar.signOut.user', { username: session.username }),
  );

  let settingsMode = $derived(settingsSection !== undefined);
  let activeSettingsEntry = $derived(
    settingsSection === undefined || settingsSection === ''
      ? null
      : settingsEntryForSection(settingsSection),
  );
</script>

{#if narrow && open}
  <button
    type="button"
    class="fixed inset-0 z-40 bg-ink/20"
    data-sidebar-overlay
    aria-hidden="true"
    tabindex="-1"
    onclick={onclose}></button>
{/if}

<aside
  id="primary-navigation-drawer"
  data-sidebar-mode={settingsMode ? 'settings' : 'primary'}
  class="fixed inset-y-0 left-0 z-50 flex w-60 shrink-0 {open ? 'translate-x-0' : '-translate-x-full'} flex-col border-r border-border bg-surface transition-transform duration-150 ease-out md:static md:z-auto md:translate-x-0 md:transition-none"
  aria-label={settingsMode ? t('sidebar.aria.settingsNavigation') : t('sidebar.aria.primaryNavigation')}
  aria-hidden={narrow && !open ? 'true' : undefined}
  inert={narrow && !open}>
  <button
    type="button"
    class="absolute right-3 top-4 flex size-9 items-center justify-center rounded-md text-ink-secondary transition-colors duration-150 ease-out hover:bg-raised hover:text-ink md:hidden"
    aria-label={t('sidebar.aria.closeNavigation')}
    onclick={onclose}
    bind:this={closeButton}>
    <Icon name="close" size={18} />
  </button>

  <a
    href={session.isAdmin ? '/movies' : '/discover'}
    class="flex items-center gap-3 px-4 py-6 focus:outline-none"
    onclick={closeForNavigation}>
    <!-- The mark is the accent itself, not inverse-on-a-fill (the Paper mock,
         and §6's "never solid fills"). -->
    <span class="text-accent" aria-hidden="true">
      <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M3 17h18" />
        <path d="M5 17V9l4-4h7a3 3 0 0 1 3 3v9" />
        <circle cx="8" cy="19" r="2" />
        <circle cx="17" cy="19" r="2" />
      </svg>
    </span>
    <span class="font-display text-lg font-bold tracking-tight text-ink">CARAVAN</span>
  </a>

  <nav class="flex flex-1 flex-col gap-6 overflow-y-auto px-2">
    {#snippet navLink(item: NavItem)}
      {@const active =
        isActive(item.href) || (item.alsoActiveOn ?? []).some((p) => isActive(p, true))}
      {@const badge = navBadge(item.href)}
      <!-- The accent is the box's own left border, so it wraps the rounded
           corners (the Paper mock). Inactive rows carry it transparent to
           keep every row's text on the same x. -->
      <a
        href={item.href}
        aria-current={active ? 'page' : undefined}
        class="flex items-center gap-3 rounded-md border-l-2 py-2 pl-4 pr-3 text-base transition-colors duration-150 ease-out
               {active
          ? 'border-l-accent bg-accent-tint text-accent-text'
          : 'border-l-transparent text-ink-secondary hover:bg-raised hover:text-ink'}"
        onclick={closeForNavigation}>
        <Icon name={item.icon} />
        <span class="flex-1">{t(item.label)}</span>
        {#if badge}
          <Badge tone={badge.tone} title={badge.title} class="tabular-nums">
            {badge.count}
          </Badge>
        {/if}
      </a>
    {/snippet}

    {#if settingsMode}
      <div data-settings-sidebar-navigation class="flex flex-col gap-4">
        <a
          data-settings-back
          href={session.isAdmin ? '/movies' : '/discover'}
          class="flex items-center gap-2 rounded-md px-3 py-2 text-sm font-medium text-ink-secondary transition-colors duration-150 ease-out hover:bg-raised hover:text-ink"
          onclick={closeForNavigation}>
          <Icon name="back" size={16} />
          <span>{t('sidebar.settings.back')}</span>
        </a>

        <a
          data-settings-overview
          href="/settings"
          aria-current={settingsSection === '' ? 'page' : undefined}
          class="flex items-center gap-3 rounded-md border-l-2 py-2 pl-4 pr-3 text-base transition-colors duration-150 ease-out
                 {settingsSection === ''
            ? 'border-l-accent bg-accent-tint text-accent-text'
            : 'border-l-transparent text-ink-secondary hover:bg-raised hover:text-ink'}"
          onclick={closeForNavigation}>
          <Icon name="settings" />
          <span>{t('sidebar.settings.overview')}</span>
        </a>

        {#each SETTINGS_GROUPS as group (group.category)}
          {@const categoryActive = activeSettingsEntry?.category === group.category}
          <div
            data-settings-category
            data-category-active={categoryActive || undefined}
            class="flex flex-col gap-1">
            <p class="micro-label px-2 pb-1 {categoryActive ? 'text-accent-text' : ''}">
              {settingsCategoryLabel(group.category, t)}
            </p>
            {#each group.items as item (item.route)}
              {@const active = activeSettingsEntry?.route === item.route}
              <a
                href={settingsHref(item)}
                aria-current={active ? 'page' : undefined}
                class="flex rounded-md border-l-2 py-1.5 pl-4 pr-3 text-sm transition-colors duration-150 ease-out
                       {active
                  ? 'border-l-accent bg-accent-tint font-medium text-accent-text'
                  : 'border-l-transparent text-ink-secondary hover:bg-raised hover:text-ink'}"
                onclick={closeForNavigation}>
                {settingsLabel(item, t)}
              </a>
            {/each}
          </div>
        {/each}
      </div>
    {:else}
      <div class="flex flex-col gap-1">
        <p class="micro-label px-2 pb-1">{t('sidebar.group.explore')}</p>
        {#each EXPLORE as item (item.href)}
          {@render navLink(item)}
        {/each}
      </div>

      {#if libraryItems.length > 0}
        <div class="flex flex-col gap-1">
          <p class="micro-label px-2 pb-1">{t('sidebar.group.library')}</p>
          {#each libraryItems as item (item.href)}
            {@render navLink(item)}
          {/each}
        </div>
      {/if}

      {#if session.isAdmin}
        <div class="flex flex-col gap-1">
          <p class="micro-label px-2 pb-1">{t('sidebar.group.activity')}</p>
          {#each ACTIVITY as item (item.href)}
            {@render navLink(item)}
          {/each}
        </div>

        <div class="flex flex-col gap-1">
          <p class="micro-label px-2 pb-1">{t('sidebar.group.manage')}</p>
          {#each MANAGE as item (item.href)}
            {@render navLink(item)}
          {/each}
        </div>
      {/if}
    {/if}
  </nav>

  <div class="m-2 flex flex-col gap-3 rounded-md border border-border bg-raised p-3">
    <!-- Disks, engine health and the safe-eject button are all read from
         GET /system/status, which is an admin route. A member gets the part of
         this card that is about them. -->
    {#if !session.isAdmin}
      <!-- The sign-out row below names them; there is nothing else to say. -->
    {:else if system.loading && !status}
      <Skeleton class="h-3 w-full" />
      <Skeleton class="h-3 w-2/3" />
    {:else}
      <div class="flex items-center gap-2">
        <span class="size-2 shrink-0 rounded-full {TONE_DOT[health.tone]}"></span>
        <span class="text-sm text-ink">{health.label}</span>
      </div>

      <!-- A link rather than a statement: every screen this breaks is fixed in
           one place, so the card that reports it goes there. -->
      {#each credentialRows as row (row.id)}
        <a
          href="/settings/metadata"
          class="flex items-center gap-2 rounded-sm text-sm text-warning transition-colors duration-150 ease-out hover:underline"
          title={row.reason || undefined}
          onclick={closeForNavigation}>
          <span class="size-2 shrink-0 rounded-full {TONE_DOT.warning}"></span>
          <span>{row.label}</span>
        </a>
      {/each}

      <div class="flex flex-col gap-2">
        <span
          class="min-w-0 truncate font-mono text-xs text-ink-muted"
          title={status?.storage_root}>
          {status?.storage_root || t('sidebar.storage.noRoot')}
        </span>
        <div class="flex items-baseline justify-between gap-2 text-xs">
          <span class="text-ink-secondary">{t('sidebar.storage.disk')}</span>
          <!-- The free number is the one a reader acts on (DESIGN.md §5), so
               it gets the line to itself: one nowrap value, never a wrapped
               pair with a dangling "free". The full breakdown moves to the
               tooltip. -->
          <span
            class="whitespace-nowrap font-mono text-ink"
            title={diskUsage
              ? t('sidebar.storage.usedOf', {
                  used: formatBytes(diskUsage.used),
                  total: formatBytes(diskUsage.used + diskUsage.free),
                })
              : undefined}>
            {diskUsage
              ? t('sidebar.storage.free', { free: formatBytes(diskUsage.free) })
              : t('sidebar.storage.unknown')}
          </span>
        </div>
        <ProgressBar
          value={usedFraction}
          tone={usedFraction > 0.9 ? 'danger' : usedFraction > 0.75 ? 'warning' : 'accent'}
          label={t('sidebar.storage.diskUsed')} />
      </div>

      <!-- Portable mode only: a drive that gets unplugged needs a way to be
           told first (SPEC §2.3). A server install is stopped by whatever
           started it. -->
      {#if status?.mode === 'portable'}
        <SafeShutdown />
      {/if}
    {/if}

    <!-- A username means an account is behind this browser; an open server has
         nobody to sign out (SPEC §11). -->
    {#if session.username}
      <Button
        variant="ghost"
        size="sm"
        class="w-full min-w-0 justify-start"
        disabled={auth.busy}
        onclick={() => auth.logout()}>
        <Icon name="logout" size={14} />
        <span class="min-w-0 truncate" title={signOutLabel}>{signOutLabel}</span>
      </Button>
    {/if}
  </div>
</aside>
