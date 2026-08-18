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
  import { libraryItemHref } from '../library';
  import { TONE_DOT, TONE_TEXT, TONE_TINT, type Tone } from '../status';
  import { page } from '../state/page.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { currentLocale, useI18n } from '../i18n.svelte';

  const { t, tp } = useI18n();


  type View = 'month' | 'agenda';

  let DAYS = $derived([
    t('route.calendar.daySun'), t('route.calendar.dayMon'), t('route.calendar.dayTue'),
    t('route.calendar.dayWed'), t('route.calendar.dayThu'), t('route.calendar.dayFri'),
    t('route.calendar.daySat'),
  ]);
  let MONTH_FORMAT = $derived(new Intl.DateTimeFormat(currentLocale(), { month: 'long', year: 'numeric' }));
  let DAY_FORMAT = $derived(new Intl.DateTimeFormat(currentLocale(), { weekday: 'long', month: 'long', day: 'numeric' }));
  let STATUS = $derived<Record<CalendarStatus, { label: string; tone: Tone }>>({
    downloaded: { label: t('route.calendar.statusDownloaded'), tone: 'success' },
    downloading: { label: t('route.calendar.statusDownloading'), tone: 'info' },
    missing: { label: t('route.calendar.statusMissing'), tone: 'danger' },
    unaired: { label: t('route.calendar.statusUnaired'), tone: 'neutral' },
  });

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
    const code = entry.episode_number === undefined
      ? ''
      : episodeCode(entry.season_number ?? 0, entry.episode_number);
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

  function agendaTitle(entry: CalendarEntry) {
    const title = entryTitle(entry);
    if (entry.kind === 'movie' || !entry.series_id || title.startsWith(entry.title)) return title;
    return `${entry.title} - ${title}`;
  }

  function entryHref(entry: CalendarEntry): string | undefined {
    return libraryItemHref({
      movie_id: entry.movie_id,
      series_id: entry.series_id,
      series_kind: entry.series_kind,
      season_number: entry.kind === 'episode' ? (entry.season_number ?? 0) : undefined,
      episode_number: entry.episode_number,
    });
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
      pushToast(t('route.calendar.feedRegenerated'), 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      regenerating = false;
    }
  }

  async function copyFeedURL() {
    try {
      await navigator.clipboard.writeText(feedURL);
      pushToast(t('route.calendar.feedCopied'), 'success');
    } catch {
      pushToast(t('route.calendar.feedCopyFailed'), 'danger');
    }
  }
  // The view toggle and feed button are page actions: the shared TopBar
  // renders them on the title row while this screen is on top.
  $effect(() => {
    page.actions = headerActions;
    return () => (page.actions = null);
  });
</script>

<div class="flex w-full min-w-0 max-w-[1360px] flex-col gap-5">
  <div class="flex flex-wrap items-center gap-3">
    <div class="flex flex-wrap items-center justify-center gap-1" aria-label={t('route.calendar.monthNavigation')}>
      <button
        type="button"
        aria-label={t('route.calendar.previousMonth')}
        title={t('route.calendar.previousMonth')}
        onclick={() => (month = addMonths(month, -1))}
        class="inline-flex h-7 items-center justify-center rounded-md px-2 text-sm font-medium text-ink-secondary transition-colors duration-150 ease-out hover:bg-raised hover:text-ink">
        <Icon name="back" size={14} />
      </button>
      <p class="min-w-44 text-center font-display text-lg font-semibold tracking-tight text-ink">{MONTH_FORMAT.format(month)}</p>
      <button
        type="button"
        aria-label={t('route.calendar.nextMonth')}
        title={t('route.calendar.nextMonth')}
        onclick={() => (month = addMonths(month, 1))}
        class="inline-flex h-7 items-center justify-center rounded-md px-2 text-sm font-medium text-ink-secondary transition-colors duration-150 ease-out hover:bg-raised hover:text-ink">
        <Icon name="chevronRight" size={14} />
      </button>
      <Button variant="secondary" size="sm" onclick={() => (month = todayMonth())}>{t('route.calendar.today')}</Button>
    </div>
  </div>

  <div class="flex flex-wrap items-center gap-x-4 gap-y-2 text-sm text-ink-secondary" aria-label={t('route.calendar.statusLegend')}>
    {#each Object.entries(STATUS) as [status, meta] (status)}
      <span class="inline-flex items-center gap-2"><span class="size-2 rounded-full {TONE_DOT[meta.tone]}" aria-hidden="true"></span>{meta.label}</span>
    {/each}
  </div>  {#if error && entries === null}
    <LoadError message={error} onretry={() => load(rangeStart, rangeEnd)} />
  {:else if loading && entries === null}
    <Skeleton class="h-[620px] w-full rounded-md" />
  {:else if view === 'month'}
    <section class="w-full min-w-0 max-w-full overflow-x-auto rounded-md border border-border bg-surface" aria-label={t('route.calendar.monthView')}>
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
              <p aria-current={isToday ? 'date' : undefined} class="relative mb-1 font-mono text-xs {isToday ? 'inline-flex size-5 items-center justify-center rounded-full bg-accent text-ink-inverse' : inMonth ? 'text-ink-secondary' : 'text-ink-muted'}">{#if isToday}<span class="sr-only">{t('route.calendar.todayPrefix')}</span>{/if}{date.getDate()}</p>
              <div class="flex flex-col gap-1">
                {#each cellEntries.slice(0, 3) as entry (entry.kind + entry.title + entry.date)}
                  {@const meta = STATUS[entry.status]}
                  {@const href = entryHref(entry)}
                  {#if href}
                    <a
                      {href}
                      class="flex min-w-0 items-center gap-1.5 rounded-sm px-1.5 py-0.5 text-xs {TONE_TINT[meta.tone]} {TONE_TEXT[meta.tone]}"
                      title={entryTitle(entry)}
                      aria-label={entryLabel(entry, meta.label)}>
                      <span class="min-w-0 truncate">{entryTitle(entry)}</span>
                      <span class="shrink-0 font-medium">{meta.label}</span>
                    </a>
                  {:else}
                    <span
                      class="flex min-w-0 items-center gap-1.5 rounded-sm px-1.5 py-0.5 text-xs {TONE_TINT[meta.tone]} {TONE_TEXT[meta.tone]}"
                      title={entryTitle(entry)}>
                      <span class="min-w-0 truncate">{entryTitle(entry)}</span>
                      <span class="shrink-0 font-medium">{meta.label}</span>
                    </span>
                  {/if}
                {/each}
                {#if cellEntries.length > 3}
                  <span class="px-1.5 text-xs text-ink-muted">{tp('route.calendar.more', cellEntries.length - 3)}</span>
                {/if}
              </div>
            </div>
          {/each}
        </div>
      </div>
    </section>
  {:else if agenda.length === 0}
    <EmptyState icon="inbox" title={t('route.calendar.emptyTitle')} message={t('route.calendar.emptyMessage')} />
  {:else}
    <section class="flex max-w-4xl flex-col gap-5" aria-label={t('route.calendar.agenda')}>
      {#each agenda as [date, dayEntries] (date)}
        <div class="flex flex-col gap-2">
          <h2 class="font-display text-lg font-semibold tracking-tight text-ink">{DAY_FORMAT.format(new Date(`${date}T12:00:00`))}</h2>
          <ul class="overflow-hidden rounded-md border border-border bg-surface">
            {#each dayEntries as entry (entry.kind + entry.title + entry.date)}
              {@const meta = STATUS[entry.status]}
              {@const href = entryHref(entry)}
              <li class="flex items-center gap-3 border-b border-border px-3 py-3 last:border-b-0">
                <span class="size-2 shrink-0 rounded-full {TONE_DOT[meta.tone]}" title={meta.label}></span>
                <Badge tone={entry.kind === 'movie' ? 'neutral' : 'info'}>{entry.kind === 'movie' ? t('route.calendar.movie') : t('route.calendar.episode')}</Badge>
                {#if href}
                  <a {href} class="min-w-0 flex-1 truncate font-medium text-ink hover:text-accent-text" title={agendaTitle(entry)}>{agendaTitle(entry)}</a>
                {:else}
                  <span class="min-w-0 flex-1 truncate font-medium text-ink" title={agendaTitle(entry)}>{agendaTitle(entry)}</span>
                {/if}
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
  <Modal title={t('route.calendar.feedTitle')} width="max-w-lg" onclose={() => (feedOpen = false)}>
    <div class="flex flex-col gap-4 p-4">
      <p class="text-base text-ink-secondary">{t('route.calendar.feedDescription')}</p>
      {#if feedLoading}
        <Skeleton class="h-9 w-full" />
      {:else if apiKey}
        <label class="micro-label" for="calendar-feed-url">{t('route.calendar.feedUrl')}</label>
        <input id="calendar-feed-url" readonly value={feedURL} class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 font-mono text-sm text-ink" />
      {:else}
        <p class="text-base text-ink-secondary">{t('route.calendar.feedNoKey')}</p>
      {/if}
    </div>
    {#snippet footer()}
      <Button variant="ghost" onclick={() => (feedOpen = false)}>{t('route.calendar.close')}</Button>
      {#if apiKey}
        <Button variant="danger" disabled={regenerating} onclick={regenerate}>{regenerating ? t('route.calendar.regenerating') : t('route.calendar.regenerateKey')}</Button>
        <Button variant="primary" onclick={copyFeedURL}>{t('route.calendar.copyUrl')}</Button>
      {:else}
        <Button variant="primary" disabled={regenerating || feedLoading} onclick={regenerate}>{regenerating ? t('route.calendar.generating') : t('route.calendar.generateKey')}</Button>
      {/if}
    {/snippet}
  </Modal>
{/if}

{#snippet headerActions()}
  <div class="flex rounded-md border border-border bg-surface p-0.5" role="group" aria-label={t('route.calendar.view')}>
    <button type="button" aria-pressed={view === 'month'} onclick={() => (view = 'month')} class="rounded-sm px-2 py-1 text-sm {view === 'month' ? 'bg-raised text-ink' : 'text-ink-secondary hover:text-ink'}">{t('route.calendar.month')}</button>
    <button type="button" aria-pressed={view === 'agenda'} onclick={() => (view = 'agenda')} class="rounded-sm px-2 py-1 text-sm {view === 'agenda' ? 'bg-raised text-ink' : 'text-ink-secondary hover:text-ink'}">{t('route.calendar.agendaTab')}</button>
  </div>
  <Button variant="secondary" onclick={openFeed}>
    <Icon name="link" size={14} />
    {t('route.calendar.feedTitle')}
  </Button>
{/snippet}
