<script lang="ts">
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { ActivityEvent, EventLevel, Job, JobState } from '../api/types';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import LoadError from '../components/LoadError.svelte';
  import PageTabs from '../components/PageTabs.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import { formatAge } from '../format';
  import { TONE_DOT, type Tone } from '../status';

  type Tab = 'events' | 'jobs';

  const TABS: { key: Tab; label: string }[] = [
    { key: 'events', label: 'Events' },
    { key: 'jobs', label: 'Jobs' },
  ];

  const EVENT_TONE: Record<EventLevel, Tone> = {
    info: 'info',
    warn: 'warning',
    error: 'danger',
  };
  const JOB_META: Record<JobState, { label: string; tone: Tone }> = {
    pending: { label: 'Pending', tone: 'neutral' },
    running: { label: 'Running', tone: 'info' },
    done: { label: 'Done', tone: 'success' },
    failed: { label: 'Failed', tone: 'danger' },
  };
  const JOB_KIND: Record<Job['kind'], string> = {
    rss_sync: 'RSS sync',
    backlog_sweep: 'Backlog sweep',
    search_movie: 'Movie search',
    search_episode: 'Episode search',
  };
  const POLL_MS = 10_000;
  const RECOVERY_MESSAGE = 'Database verified after an unclean shutdown';

  type ActivityEventRow = ActivityEvent & { repeatCount: number };

  function sameRecoveryEvent(left: ActivityEvent, right: ActivityEvent): boolean {
    return left.message === RECOVERY_MESSAGE &&
      right.message === RECOVERY_MESSAGE &&
      left.level === right.level &&
      left.category === right.category &&
      left.detail === right.detail &&
      left.movie_id === right.movie_id &&
      left.series_id === right.series_id;
  }

  function coalesceRecoveryEvents(items: ActivityEvent[]): ActivityEventRow[] {
    const rows: ActivityEventRow[] = [];
    for (const event of items) {
      const previous = rows.at(-1);
      if (previous && sameRecoveryEvent(previous, event)) {
        previous.repeatCount += 1;
        continue;
      }
      rows.push({ ...event, repeatCount: 1 });
    }
    return rows;
  }

  let tab = $state<Tab>('events');
  let events = $state<ActivityEvent[] | null>(null);
  let jobs = $state<Job[] | null>(null);
  let eventsNextCursor = $state('');
  let jobsNextCursor = $state('');
  let eventsLoadedOlder = $state(false);
  let jobsLoadedOlder = $state(false);
  let eventsError = $state<string | null>(null);
  let jobsError = $state<string | null>(null);
  let eventsLoading = $state(true);
  let jobsLoading = $state(false);
  let eventsLoadingOlder = $state(false);
  let jobsLoadingOlder = $state(false);
  let eventRows = $derived(coalesceRecoveryEvents(events ?? []));

  function mergeByID<T extends { id: number }>(current: T[], incoming: T[]): T[] {
    const seen = new Set<number>();
    return [...incoming, ...current]
      .filter((item) => !seen.has(item.id) && seen.add(item.id))
      .sort((a, b) => b.id - a.id);
  }

  async function loadEvents(older = false) {
    if (older) {
      if (!eventsNextCursor || eventsLoadingOlder) return;
      eventsLoadingOlder = true;
    } else {
      eventsLoading = true;
    }
    try {
      const page = await api.listEventsPage(100, older ? eventsNextCursor : undefined);
      if (older) {
        events = mergeByID(events ?? [], page.events);
        eventsNextCursor = page.next_cursor;
        eventsLoadedOlder = true;
      } else if (events === null) {
        events = page.events;
        eventsNextCursor = page.next_cursor;
      } else {
        events = mergeByID(events, page.events);
        if (!eventsLoadedOlder) eventsNextCursor = page.next_cursor;
      }
      eventsError = null;
    } catch (err) {
      eventsError = errorText(err);
    } finally {
      if (older) eventsLoadingOlder = false;
      else eventsLoading = false;
    }
  }

  async function loadJobs(older = false) {
    if (older) {
      if (!jobsNextCursor || jobsLoadingOlder) return;
      jobsLoadingOlder = true;
    } else {
      jobsLoading = true;
    }
    try {
      const page = await api.listJobsPage(100, older ? jobsNextCursor : undefined);
      if (older) {
        jobs = mergeByID(jobs ?? [], page.jobs);
        jobsNextCursor = page.next_cursor;
        jobsLoadedOlder = true;
      } else if (jobs === null) {
        jobs = page.jobs;
        jobsNextCursor = page.next_cursor;
      } else {
        jobs = mergeByID(jobs, page.jobs);
        if (!jobsLoadedOlder) jobsNextCursor = page.next_cursor;
      }
      jobsError = null;
    } catch (err) {
      jobsError = errorText(err);
    } finally {
      if (older) jobsLoadingOlder = false;
      else jobsLoading = false;
    }
  }

  onMount(() => {
    loadEvents();
    const timer = window.setInterval(() => {
      if (tab === 'events') loadEvents();
      else loadJobs();
    }, POLL_MS);
    return () => window.clearInterval(timer);
  });

  $effect(() => {
    if (tab === 'jobs' && jobs === null && !jobsLoading) loadJobs();
  });

</script>
<div class="flex max-w-4xl flex-col gap-6">
  <div class="flex items-center gap-2">
    <div class="flex-1">
      <PageTabs
        tabs={TABS}
        active={tab}
        onchange={(key) => (tab = key)}
        ariaLabel="Activity feed" />
    </div>
    <Button variant="secondary" size="sm" onclick={() => (tab === 'events' ? loadEvents() : loadJobs())}>
      Refresh
    </Button>
  </div>

  {#if tab === 'events'}
    {#if eventsError && events === null}
      <LoadError message={eventsError} onretry={loadEvents} />
    {:else if eventsLoading && events === null}
      <div class="flex flex-col gap-2">{#each Array.from({ length: 4 }) as _, i (i)}<Skeleton class="h-16 w-full rounded-md" />{/each}</div>
    {:else if (events ?? []).length === 0}
      <EmptyState
        icon="pulse"
        title="No activity recorded"
        message="Current health appears in the system status panel. Imports, searches and scheduled work will appear here." />
    {:else}
      <ol class="flex flex-col gap-2" aria-label="Activity events">
        {#each eventRows as event (event.id)}
          <li class="rounded-md border border-border bg-surface px-3 py-3">
            <div class="flex items-start gap-3">
              <span class="mt-1.5 size-2 shrink-0 rounded-full {TONE_DOT[EVENT_TONE[event.level]]}" aria-hidden="true"></span>
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <Badge tone={EVENT_TONE[event.level]}>{event.category}</Badge>
                  <span class="font-mono text-xs uppercase text-ink-secondary">{event.level}</span>
                  <p class="text-base text-ink">{event.message}</p>
                  {#if event.repeatCount > 1}
                    <Badge tone="neutral">{event.repeatCount} times</Badge>
                  {/if}
                  <time class="ml-auto text-sm text-ink-muted" datetime={event.created_at} title={event.created_at}>{formatAge(event.created_at)} ago</time>
                </div>
                {#if event.detail}
                  <details class="mt-2 text-sm text-ink-secondary">
                    <summary class="cursor-pointer text-ink-secondary hover:text-ink">Details</summary>
                    <p class="mt-2 whitespace-pre-wrap">{event.detail}</p>
                  </details>
                {/if}
              </div>
            </div>
          </li>
        {/each}
      </ol>
      {#if eventsNextCursor || eventsLoadingOlder}
        <div class="mt-3 flex justify-center">
          <Button size="sm" disabled={eventsLoadingOlder || !eventsNextCursor} onclick={() => loadEvents(true)}>
            {eventsLoadingOlder ? 'Loading...' : 'Load older'}
          </Button>
        </div>
      {/if}
    {/if}
  {:else if jobsError && jobs === null}
    <LoadError message={jobsError} onretry={loadJobs} />
  {:else if jobsLoading && jobs === null}
    <div class="flex flex-col gap-2">{#each Array.from({ length: 4 }) as _, i (i)}<Skeleton class="h-16 w-full rounded-md" />{/each}</div>
  {:else if (jobs ?? []).length === 0}
    <EmptyState icon="pulse" title="No jobs yet" message="Scheduled acquisition work will appear here." />
  {:else}
    <ul class="overflow-hidden rounded-md border border-border bg-surface" aria-label="Acquisition jobs">
      {#each jobs ?? [] as job (job.id)}
        {@const meta = JOB_META[job.state]}
        <li class="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-border px-3 py-3 last:border-b-0">
          <span class="size-2 shrink-0 rounded-full {TONE_DOT[meta.tone]}" aria-hidden="true"></span>
          <p class="min-w-36 font-medium text-ink">{JOB_KIND[job.kind]}</p>
          <Badge tone={meta.tone}>{meta.label}</Badge>
          <span class="font-mono text-xs text-ink-secondary">{job.attempts}/5</span>
          <time class="ml-auto text-sm text-ink-muted" datetime={job.updated_at || job.created_at} title={job.updated_at || job.created_at}>{formatAge(job.updated_at || job.created_at)} ago</time>
          {#if job.state === 'failed' && job.last_error}
            <p class="w-full pl-5 text-sm text-danger">{job.last_error}</p>
          {/if}
        </li>
      {/each}
    </ul>
    {#if jobsNextCursor || jobsLoadingOlder}
      <div class="mt-3 flex justify-center">
        <Button size="sm" disabled={jobsLoadingOlder || !jobsNextCursor} onclick={() => loadJobs(true)}>
          {jobsLoadingOlder ? 'Loading...' : 'Load older'}
        </Button>
      </div>
    {/if}
  {/if}
</div>
