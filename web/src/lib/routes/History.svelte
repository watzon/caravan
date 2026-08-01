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

  let tab = $state<Tab>('events');
  let events = $state<ActivityEvent[] | null>(null);
  let jobs = $state<Job[] | null>(null);
  let eventsError = $state<string | null>(null);
  let jobsError = $state<string | null>(null);
  let eventsLoading = $state(true);
  let jobsLoading = $state(false);

  async function loadEvents() {
    eventsLoading = true;
    try {
      events = await api.listEvents();
      eventsError = null;
    } catch (err) {
      eventsError = errorText(err);
    } finally {
      eventsLoading = false;
    }
  }

  async function loadJobs() {
    jobsLoading = true;
    try {
      jobs = await api.listJobs();
      jobsError = null;
    } catch (err) {
      jobsError = errorText(err);
    } finally {
      jobsLoading = false;
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
      <EmptyState icon="pulse" title="No activity yet" message="Imports, searches and scheduled work will appear here." />
    {:else}
      <ol class="flex flex-col gap-2" aria-label="Activity events">
        {#each events ?? [] as event (event.id)}
          <li class="rounded-md border border-border bg-surface px-3 py-3">
            <div class="flex items-start gap-3">
              <span class="mt-1.5 size-2 shrink-0 rounded-full {TONE_DOT[EVENT_TONE[event.level]]}" title={event.level}></span>
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <Badge tone={EVENT_TONE[event.level]}>{event.category}</Badge>
                  <p class="text-base text-ink">{event.message}</p>
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
          <span class="size-2 shrink-0 rounded-full {TONE_DOT[meta.tone]}" title={meta.label}></span>
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
  {/if}
</div>
