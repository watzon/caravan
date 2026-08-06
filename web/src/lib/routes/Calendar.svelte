<script lang="ts">
  import { api, endpoints, errorText } from '../api/client';
  import type { CalendarEntry, CalendarStatus, Settings } from '../api/types';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import Modal from '../components/Modal.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import { episodeCode } from '../format';
  import { TONE_DOT, TONE_TEXT, TONE_TINT, type Tone } from '../status';
  import { page } from '../state/page.svelte';
  import { pushToast } from '../state/toast.svelte';

  type View = 'month' | 'agenda';

  const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
  const MONTH_FORMAT = new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric' });
  const DAY_FORMAT = new Intl.DateTimeFormat(undefined, { weekday: 'long', month: 'long', day: 'numeric' });
  const STATUS: Record<CalendarStatus, { label: string; tone: Tone }> = {
    downloaded: { label: 'On disk', tone: 'success' },
    downloading: { label: 'Downloading', tone: 'info' },
    missing: { label: 'Missing', tone: 'danger' },
    unaired: { label: 'Not yet released', tone: 'neutral' },
  };

  function todayMonth() {
    const now = new Date();
    return new Date(now.getFullYear(), now.getMonth(), 1);
  }

  function addMonths(date: Date, amount: number) {
    return new Date(date.getFullYear(), date.getMonth() + amount, 1);
  }

  function iso(date: Date) {
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    return `${year}-${month}-${day}`;
  }

  function monthCells(month: Date) {
    const start = new Date(month.getFullYear(), month.getMonth(), 1);
    start.setDate(start.getDate() - start.getDay());
    return Array.from({ length: 42 }, (_, index) => {
      const date = new Date(start);
      date.setDate(start.getDate() + index);
      return date;
    });
  }

  function entryTitle(entry: CalendarEntry) {
    if (entry.kind === 'movie') return entry.title;
    const code = entry.season_number === undefined || entry.episode_number === undefined
      ? ''
      : episodeCode(entry.season_number, entry.episode_number);
    const episodeTitle = entry.episode_title?.trim() ?? '';
    if (!episodeTitle || /^Episode(?:\s+\d+)?$/i.test(episodeTitle)) {
      return [entry.title, code].filter(Boolean).join(' ');
    }
    return [code, episodeTitle].filter(Boolean).join(' ');
  }

  function entryLabel(entry: CalendarEntry, status: string) {
    const title = entryTitle(entry);
    const identified = entry.kind === 'episode' && !title.startsWith(entry.title)
      ? `${entry.title} ${title}`
      : title;
    return `${identified}, ${status}`;
  }

  let month = $state(todayMonth());
  let view = $state<View>('month');
  let entries = $state<CalendarEntry[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let feedOpen = $state(false);
  let settings = $state<Settings | null>(null);
  let feedLoading = $state(false);
  let regenerating = $state(false);

  let cells = $derived(monthCells(month));
  let rangeStart = $derived(iso(cells[0]!));
  let rangeEnd = $derived(iso(new Date(cells[cells.length - 1]!.getFullYear(), cells[cells.length - 1]!.getMonth(), cells[cells.length - 1]!.getDate() + 1)));
  let byDate = $derived.by(() => {
    const grouped = new Map<string, CalendarEntry[]>();
    for (const entry of entries ?? []) {
      const group = grouped.get(entry.date) ?? [];
      group.push(entry);
      grouped.set(entry.date, group);
    }
    return grouped;
  });
  let agenda = $derived([...byDate.entries()].sort(([left], [right]) => left.localeCompare(right)));
  let apiKey = $derived(settings?.api_key ?? '');
  let feedURL = $derived(apiKey ? `${window.location.origin}${endpoints.calendarFeed(apiKey)}` : '');

  async function load(start: string, end: string) {
    loading = true;
    try {
      entries = await api.calendar(start, end);
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    const start = rangeStart;
    const end = rangeEnd;
    load(start, end);
  });

  async function openFeed() {
    feedOpen = true;
    feedLoading = true;
    try {
      settings = await api.getSettings();
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      feedLoading = false;
    }
  }

  async function regenerate() {
    regenerating = true;
    try {
      const response = await api.regenerateAPIKey();
      settings = { ...(settings ?? {}), api_key: response.api_key };
      pushToast('Calendar feed key regenerated.', 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      regenerating = false;
    }
  }

  async function copyFeedURL() {
    try {
      await navigator.clipboard.writeText(feedURL);
      pushToast('Calendar feed URL copied.', 'success');
    } catch {
      pushToast('Could not copy the calendar feed URL.', 'danger');
    }
  }
  // The view toggle and feed button are page actions: the shared TopBar
  // renders them on the title row while this screen is on top.
  $effect(() => {
    page.actions = headerActions;
    return () => (page.actions = null);
  });
</script>

<div class="flex max-w-[1360px] flex-col gap-5">
  <div class="flex flex-wrap items-center gap-3">
    <div class="flex items-center gap-1" aria-label="Calendar month navigation">
      <button
        type="button"
        aria-label="Previous month"
        title="Previous month"
        onclick={() => (month = addMonths(month, -1))}
        class="inline-flex h-7 items-center justify-center rounded-md px-2 text-sm font-medium text-ink-secondary transition-colors duration-150 ease-out hover:bg-raised hover:text-ink">
        <Icon name="back" size={14} />
      </button>
      <p class="min-w-44 text-center font-display text-lg font-semibold tracking-tight text-ink">{MONTH_FORMAT.format(month)}</p>
      <button
        type="button"
        aria-label="Next month"
        title="Next month"
        onclick={() => (month = addMonths(month, 1))}
        class="inline-flex h-7 items-center justify-center rounded-md px-2 text-sm font-medium text-ink-secondary transition-colors duration-150 ease-out hover:bg-raised hover:text-ink">
        <Icon name="chevronRight" size={14} />
      </button>
      <Button variant="secondary" size="sm" onclick={() => (month = todayMonth())}>Today</Button>
    </div>
  </div>

  <div class="flex flex-wrap items-center gap-x-4 gap-y-2 text-sm text-ink-secondary" aria-label="Calendar status legend">
    {#each Object.entries(STATUS) as [status, meta] (status)}
      <span class="inline-flex items-center gap-2"><span class="size-2 rounded-full {TONE_DOT[meta.tone]}" aria-hidden="true"></span>{meta.label}</span>
    {/each}
  </div>  {#if error && entries === null}
    <LoadError message={error} onretry={() => load(rangeStart, rangeEnd)} />
  {:else if loading && entries === null}
    <Skeleton class="h-[620px] w-full rounded-md" />
  {:else if view === 'month'}
    <section class="overflow-x-auto rounded-md border border-border bg-surface" aria-label="Month calendar">
      <div class="min-w-[760px]">
        <div class="grid grid-cols-7 border-b border-border bg-raised">
          {#each DAYS as day}
            <div class="px-3 py-2 text-xs font-semibold text-ink-secondary">{day}</div>
          {/each}
        </div>
        <div class="grid grid-cols-7">
          {#each cells as date (iso(date))}
            {@const dateKey = iso(date)}
            {@const cellEntries = byDate.get(dateKey) ?? []}
            {@const inMonth = date.getMonth() === month.getMonth()}
            {@const isToday = dateKey === iso(new Date())}
            <div
              data-today={isToday ? 'true' : undefined}
              class="min-h-28 border-b border-r border-border p-2 last:border-r-0 {isToday ? 'bg-accent-tint' : inMonth ? 'bg-surface' : 'bg-bg/40'}">
              <p class="mb-1 font-mono text-xs {isToday ? 'inline-flex size-5 items-center justify-center rounded-full bg-accent text-ink-inverse' : inMonth ? 'text-ink-secondary' : 'text-ink-muted'}">{date.getDate()}</p>
              <div class="flex flex-col gap-1">
                {#each cellEntries.slice(0, 3) as entry (entry.kind + entry.title + entry.date)}
                  {@const meta = STATUS[entry.status]}
                  <a
                    href={entry.kind === 'movie' && entry.movie_id ? `/movies/${entry.movie_id}` : entry.series_id ? `/series/${entry.series_id}` : undefined}
                    class="truncate rounded-sm px-1.5 py-0.5 text-xs {TONE_TINT[meta.tone]} {TONE_TEXT[meta.tone]}"
                    title={entryTitle(entry)}
                    aria-label={entryLabel(entry, meta.label)}>
                    {entryTitle(entry)}
                  </a>
                {/each}
                {#if cellEntries.length > 3}
                  <span class="px-1.5 text-xs text-ink-muted">+{cellEntries.length - 3} more</span>
                {/if}
              </div>
            </div>
          {/each}
        </div>
      </div>
    </section>
  {:else if agenda.length === 0}
    <EmptyState icon="inbox" title="Nothing scheduled" message="Upcoming movie and episode availability will appear here." />
  {:else}
    <section class="flex max-w-4xl flex-col gap-5" aria-label="Calendar agenda">
      {#each agenda as [date, dayEntries] (date)}
        <div class="flex flex-col gap-2">
          <h2 class="font-display text-lg font-semibold tracking-tight text-ink">{DAY_FORMAT.format(new Date(`${date}T12:00:00`))}</h2>
          <ul class="overflow-hidden rounded-md border border-border bg-surface">
            {#each dayEntries as entry (entry.kind + entry.title + entry.date)}
              {@const meta = STATUS[entry.status]}
              <li class="flex items-center gap-3 border-b border-border px-3 py-3 last:border-b-0">
                <span class="size-2 shrink-0 rounded-full {TONE_DOT[meta.tone]}" title={meta.label}></span>
                <Badge tone={entry.kind === 'movie' ? 'neutral' : 'info'}>{entry.kind === 'movie' ? 'Movie' : 'Episode'}</Badge>
                <a href={entry.kind === 'movie' && entry.movie_id ? `/movies/${entry.movie_id}` : entry.series_id ? `/series/${entry.series_id}` : undefined} class="min-w-0 flex-1 truncate font-medium text-ink hover:text-accent-text">{entry.kind === 'episode' && entry.series_id ? `${entry.title} - ${entryTitle(entry)}` : entryTitle(entry)}</a>
                <span class="text-sm {TONE_TEXT[meta.tone]}">{meta.label}</span>
              </li>
            {/each}
          </ul>
        </div>
      {/each}
    </section>
  {/if}
</div>

{#if feedOpen}
  <Modal title="iCal feed" width="max-w-lg" onclose={() => (feedOpen = false)}>
    <div class="flex flex-col gap-4 p-4">
      <p class="text-base text-ink-secondary">Use this private URL in a calendar app to see monitored releases. Regenerating the key invalidates every existing feed URL.</p>
      {#if feedLoading}
        <Skeleton class="h-9 w-full" />
      {:else if apiKey}
        <label class="micro-label" for="calendar-feed-url">Feed URL</label>
        <input id="calendar-feed-url" readonly value={feedURL} class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink" />
      {:else}
        <p class="text-base text-ink-secondary">Generate a private key to create a feed URL.</p>
      {/if}
    </div>
    {#snippet footer()}
      <Button variant="ghost" onclick={() => (feedOpen = false)}>Close</Button>
      {#if apiKey}
        <Button variant="danger" disabled={regenerating} onclick={regenerate}>{regenerating ? 'Regenerating...' : 'Regenerate key'}</Button>
        <Button variant="primary" onclick={copyFeedURL}>Copy URL</Button>
      {:else}
        <Button variant="primary" disabled={regenerating || feedLoading} onclick={regenerate}>{regenerating ? 'Generating...' : 'Generate key'}</Button>
      {/if}
    {/snippet}
  </Modal>
{/if}

{#snippet headerActions()}
  <div class="flex rounded-md border border-border bg-surface p-0.5" role="group" aria-label="Calendar view">
    <button type="button" aria-pressed={view === 'month'} onclick={() => (view = 'month')} class="rounded-sm px-2 py-1 text-sm {view === 'month' ? 'bg-raised text-ink' : 'text-ink-secondary hover:text-ink'}">Month</button>
    <button type="button" aria-pressed={view === 'agenda'} onclick={() => (view = 'agenda')} class="rounded-sm px-2 py-1 text-sm {view === 'agenda' ? 'bg-raised text-ink' : 'text-ink-secondary hover:text-ink'}">Agenda</button>
  </div>
  <Button variant="secondary" onclick={openFeed}>
    <Icon name="link" size={14} />
    iCal feed
  </Button>
{/snippet}
