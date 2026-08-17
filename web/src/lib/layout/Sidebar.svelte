<script lang="ts">
  /**
   * DESIGN.md §5: fixed 240px sidebar on --color-surface with a hairline right
   * border. Nav is phase-gated: Wanted and Calendar arrived with phase 3 and
   * Convert with phase 4, so every entry here has a screen behind it.
   *
   * The persistent bottom slot is a live task rail (what is running, or
   * the last failure) plus sign-out — not a status card.
   *
   * A member sees the Explore group and nothing else: the other three lead to
   * screens the server answers 403 for (SPEC §11). The one exception is the
   * Adult shelf, which a granted member may read — so their Library group
   * holds that row alone rather than being suppressed wholesale.
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
  import { downloads } from '../state/downloads.svelte';
  import { requests } from '../state/requests.svelte';
  import { tasks } from '../state/tasks.svelte';
  import { footerStack } from '../tasks';
  import { system } from '../state/system.svelte';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import Icon, { type IconName } from '../components/Icon.svelte';
  import SafeShutdown from '../components/SafeShutdown.svelte';
  import type { Tone } from '../status';
  import {
    readDisplayPreferences,
    resolvedTheme,
    toggleResolvedTheme,
  } from '../displayPreferences';
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
  let theme = $state<'dark' | 'light'>('dark');
  let themeLabel = $derived(
    theme === 'dark' ? t('sidebar.theme.toLight') : t('sidebar.theme.toDark'),
  );

  onMount(() => {
    theme = resolvedTheme(readDisplayPreferences().theme);
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

  function flipTheme() {
    theme = resolvedTheme(toggleResolvedTheme().theme);
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

  // Prime the stores the badges read. After that, local writes and the
  // live stream update them — the shell does not poll.
  $effect(() => {
    void requests.refresh();
    if (!session.isAdmin) return;
    void downloads.refresh();
    void tasks.refresh();
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
      case '/settings': {
        const count = tasks.issueCount;
        return count
          ? { count, tone: 'warning', title: tp('sidebar.badge.taskIssue', count) }
          : null;
      }
      default:
        return null;
    }
  }

  let stack = $derived(
    footerStack({
      tasks: tasks.tasks,
      jobs: tasks.jobs,
      downloads: downloads.items,
      converting: status?.counts.converting ?? 0,
    }),
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
                class="flex items-center gap-2 rounded-md border-l-2 py-1.5 pl-4 pr-3 text-sm transition-colors duration-150 ease-out
                       {active
                  ? 'border-l-accent bg-accent-tint font-medium text-accent-text'
                  : 'border-l-transparent text-ink-secondary hover:bg-raised hover:text-ink'}"
                onclick={closeForNavigation}>
                <span class="min-w-0 flex-1">{settingsLabel(item, t)}</span>
                {#if item.route === '/settings/tasks' && tasks.issueCount}
                  <Badge
                    tone="warning"
                    title={tp('sidebar.badge.taskIssue', tasks.issueCount)}
                    class="tabular-nums">
                    {tasks.issueCount}
                  </Badge>
                {/if}
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

  <div data-sidebar-footer class="flex flex-col gap-1 border-t border-border px-2 py-3">
    {#if session.isAdmin && stack.length > 0}
      <div data-sidebar-activity class="flex flex-col gap-0.5" aria-live="polite">
        {#each stack as item (item.id)}
          <a
            data-sidebar-activity-row
            href={item.href}
            title={item.title}
            class="flex items-center gap-2 rounded-md px-3 py-1.5 text-sm transition-colors duration-150 ease-out
                   {item.tone === 'warning'
              ? 'text-warning hover:bg-raised'
              : 'text-accent-text hover:bg-raised'}"
            onclick={closeForNavigation}>
            <span
              class="size-1.5 shrink-0 rounded-full
                     {item.tone === 'warning' ? 'bg-warning' : 'bg-accent'}
                     {item.spinning ? 'sidebar-task-pulse' : ''}"
              aria-hidden="true"></span>
            <span class="min-w-0 truncate">{item.label}</span>
          </a>
        {/each}
      </div>
    {/if}

    {#if session.isAdmin && status?.mode === 'portable'}
      <div class="px-3">
        <SafeShutdown />
      </div>
    {/if}

    <div class="flex items-center gap-1">
      {#if session.username}
        <Button
          variant="ghost"
          size="sm"
          class="min-w-0 flex-1 justify-start text-ink-muted"
          disabled={auth.busy}
          onclick={() => auth.logout()}>
          <Icon name="logout" size={14} />
          <span class="min-w-0 truncate" title={signOutLabel}>{signOutLabel}</span>
        </Button>
      {/if}
      <button
        type="button"
        data-sidebar-theme
        class="flex size-7 shrink-0 items-center justify-center rounded-md text-ink-muted transition-colors duration-150 ease-out hover:bg-raised hover:text-ink"
        aria-label={themeLabel}
        title={themeLabel}
        onclick={flipTheme}>
        <Icon name={theme === 'dark' ? 'sun' : 'moon'} size={14} />
      </button>
    </div>
  </div>
</aside>
