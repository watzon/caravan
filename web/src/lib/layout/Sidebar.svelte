<script lang="ts">
  /**
   * DESIGN.md §5: fixed 240px sidebar on --color-surface with a hairline right
   * border. Nav is phase-gated: Wanted and Calendar arrived with phase 3 and
   * Convert with phase 4, so every entry here has a screen behind it.
   *
   * The persistent bottom slot holds system status (disk free, engine health).
   */
  import { isActive } from '../router.svelte';
  import { auth } from '../state/auth.svelte';
  import { BADGE_POLL_MS, downloads } from '../state/downloads.svelte';
  import { system } from '../state/system.svelte';
  import { formatBytes } from '../format';
  import Icon, { type IconName } from '../components/Icon.svelte';
  import ProgressBar from '../components/ProgressBar.svelte';
  import SafeShutdown from '../components/SafeShutdown.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import type { Tone } from '../status';
  import { TONE_DOT } from '../status';

  interface NavItem {
    href: string;
    label: string;
    icon: IconName;
  }

  const LIBRARY: NavItem[] = [
    { href: '/movies', label: 'Movies', icon: 'film' },
    { href: '/series', label: 'Series', icon: 'tv' },
    { href: '/wanted', label: 'Wanted', icon: 'search' },
    { href: '/calendar', label: 'Calendar', icon: 'inbox' },
  ];

  const ACTIVITY: NavItem[] = [
    { href: '/queue', label: 'Queue', icon: 'download' },
    { href: '/convert', label: 'Convert', icon: 'refresh' },
    { href: '/history', label: 'History', icon: 'pulse' },
  ];

  const MANAGE: NavItem[] = [
    { href: '/scan-review', label: 'Scan Review', icon: 'inbox' },
    { href: '/settings', label: 'Settings', icon: 'settings' },
  ];

  // The badge is the only reason the shell polls downloads, so it does so
  // lazily; the queue screen subscribes at its own faster rate while open.
  $effect(() => downloads.subscribe(BADGE_POLL_MS));

  let status = $derived(system.status);

  let usedFraction = $derived.by(() => {
    const s = status;
    if (!s || s.disk_total_bytes <= 0) return 0;
    return (s.disk_total_bytes - s.disk_free_bytes) / s.disk_total_bytes;
  });

  let health = $derived.by((): { tone: Tone; label: string } => {
    if (system.error) return { tone: 'danger', label: 'Unreachable' };
    const s = status;
    if (!s) return { tone: 'neutral', label: 'Checking…' };
    if (s.dirty) return { tone: 'danger', label: 'Dirty shutdown' };
    if (s.engine_health === 'ok') return { tone: 'success', label: 'Healthy' };
    if (s.engine_health === 'degraded') return { tone: 'warning', label: 'Degraded' };
    if (s.engine_health === 'error') return { tone: 'danger', label: 'Engine error' };
    // "unconfigured": no storage root yet, so no engine — a setup state, not a failure.
    return { tone: 'neutral', label: 'Not set up' };
  });
</script>

<aside
  class="flex w-60 shrink-0 flex-col border-r border-border bg-surface"
  aria-label="Primary navigation">
  <a href="/movies" class="flex items-center gap-3 px-4 py-6 focus:outline-none">
    <span
      class="flex size-8 items-center justify-center rounded-md bg-accent text-ink-inverse"
      aria-hidden="true">
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M3 17h18" />
        <path d="M5 17V9l4-4h7a3 3 0 0 1 3 3v9" />
        <circle cx="8" cy="19" r="2" />
        <circle cx="17" cy="19" r="2" />
      </svg>
    </span>
    <span class="font-display text-lg font-bold tracking-tight text-ink">CARAVAN</span>
  </a>

  <nav class="flex flex-1 flex-col gap-6 overflow-y-auto px-2">
    <div class="flex flex-col gap-1">
      <p class="micro-label px-2 pb-1">Library</p>
      {#each LIBRARY as item (item.href)}
        {@const active = isActive(item.href)}
        <a
          href={item.href}
          aria-current={active ? 'page' : undefined}
          class="relative flex items-center gap-3 rounded-md py-2 pl-4 pr-3 text-base transition-colors duration-150 ease-out
                 {active
            ? 'bg-accent-tint text-accent-text'
            : 'text-ink-secondary hover:bg-raised hover:text-ink'}">
          {#if active}
            <span class="absolute left-0 top-1 h-[calc(100%-8px)] w-0.5 rounded-full bg-accent"></span>
          {/if}
          <Icon name={item.icon} />
          <span>{item.label}</span>
        </a>
      {/each}
    </div>

    <div class="flex flex-col gap-1">
      <p class="micro-label px-2 pb-1">Activity</p>
      {#each ACTIVITY as item (item.href)}
        {@const active = isActive(item.href)}
        <a
          href={item.href}
          aria-current={active ? 'page' : undefined}
          class="relative flex items-center gap-3 rounded-md py-2 pl-4 pr-3 text-base transition-colors duration-150 ease-out
                 {active
            ? 'bg-accent-tint text-accent-text'
            : 'text-ink-secondary hover:bg-raised hover:text-ink'}">
          {#if active}
            <span class="absolute left-0 top-1 h-[calc(100%-8px)] w-0.5 rounded-full bg-accent"></span>
          {/if}
          <Icon name={item.icon} />
          <span class="flex-1">{item.label}</span>
          {#if item.href === '/queue' && downloads.activeCount > 0}
            <span
              class="inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-accent px-1.5 text-xs font-semibold text-ink-inverse"
              aria-label="{downloads.activeCount} active downloads">
              {downloads.activeCount}
            </span>
          {/if}
        </a>
      {/each}
    </div>

    <div class="flex flex-col gap-1">
      <p class="micro-label px-2 pb-1">Manage</p>
      {#each MANAGE as item (item.href)}
        {@const active = isActive(item.href)}
        <a
          href={item.href}
          aria-current={active ? 'page' : undefined}
          class="relative flex items-center gap-3 rounded-md py-2 pl-4 pr-3 text-base transition-colors duration-150 ease-out
                 {active
            ? 'bg-accent-tint text-accent-text'
            : 'text-ink-secondary hover:bg-raised hover:text-ink'}">
          {#if active}
            <span class="absolute left-0 top-1 h-[calc(100%-8px)] w-0.5 rounded-full bg-accent"></span>
          {/if}
          <Icon name={item.icon} />
          <span>{item.label}</span>
        </a>
      {/each}
    </div>
  </nav>

  <div class="m-2 flex flex-col gap-3 rounded-md border border-border bg-raised p-3">
    <p class="micro-label">System</p>

    {#if system.loading && !status}
      <Skeleton class="h-3 w-full" />
      <Skeleton class="h-3 w-2/3" />
    {:else}
      <div class="flex flex-col gap-2">
        <div class="flex items-center gap-2 text-ink-secondary">
          <Icon name="disk" size={14} />
          <span class="flex-1 text-sm">Disk free</span>
          <span class="font-mono text-xs text-ink">
            {status && status.disk_total_bytes > 0 ? formatBytes(status.disk_free_bytes) : '—'}
          </span>
        </div>
        <ProgressBar
          value={usedFraction}
          tone={usedFraction > 0.9 ? 'danger' : usedFraction > 0.75 ? 'warning' : 'accent'}
          label="Disk used" />
      </div>

      <div class="flex items-center gap-2">
        <span class="size-2 shrink-0 rounded-full {TONE_DOT[health.tone]}"></span>
        <span class="flex-1 text-sm text-ink-secondary">Engine</span>
        <span class="text-sm text-ink">{health.label}</span>
      </div>

      {#if status}
        <p class="truncate font-mono text-xs text-ink-muted" title={status.storage_root}>
          {status.mode || 'binary'} · {status.storage_root || 'no storage root'}
        </p>
      {/if}

      <!-- Portable mode only: a drive that gets unplugged needs a way to be
           told first (SPEC §2.3). A server install is stopped by whatever
           started it. -->
      {#if status?.mode === 'portable'}
        <SafeShutdown />
      {/if}

      <!-- Only meaningful when a password is set; without one there is no
           session to end (SPEC §11). -->
      {#if status?.password_set}
        <button
          type="button"
          class="flex items-center gap-2 rounded-md py-1 text-sm text-ink-secondary transition-colors duration-150 ease-out hover:text-ink disabled:opacity-50"
          disabled={auth.busy}
          onclick={() => auth.logout()}>
          <Icon name="back" size={14} />
          <span>{auth.busy ? 'Signing out…' : 'Sign out'}</span>
        </button>
      {/if}
    {/if}
  </div>
</aside>
