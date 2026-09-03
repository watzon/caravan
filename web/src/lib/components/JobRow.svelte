<script lang="ts">
  /**
   * One row of the History page's Jobs feed: a summary line that opens into
   * the job's details.
   *
   * `attempts` is a failure count, not progress. The store only moves it when
   * a run fails, so a job that succeeded first time reads 0, and the header
   * says nothing about tries until one has failed. The panel always shows the
   * count so a retried-then-succeeded job is not mistaken for a clean one.
   */
  import type { Job, JobState } from '../api/types';
  import Badge from './Badge.svelte';
  import Icon from './Icon.svelte';
  import { useI18n } from '../i18n.svelte';
  import { formatAge, formatDateTime } from '../format';
  import { TONE_DOT, type Tone } from '../status';
  import { jobKindLabel, subjectHref } from '../tasks';

  /** Mirrors store.JobMaxAttempts; the API does not report the limit. */
  const JOB_MAX_ATTEMPTS = 5;

  interface Props {
    job: Job;
    expanded: boolean;
    ontoggle: () => void;
  }

  let { job, expanded, ontoggle }: Props = $props();
  const { t } = useI18n();
  const rowID = $props.id();
  const panelID = `${rowID}-details`;

  const META: Record<JobState, { label: string; tone: Tone }> = {
    pending: { label: t('route.history.jobPending'), tone: 'neutral' },
    running: { label: t('route.history.jobRunning'), tone: 'info' },
    done: { label: t('route.history.jobDone'), tone: 'success' },
    failed: { label: t('route.history.jobFailed'), tone: 'danger' },
    cancelled: { label: t('route.history.jobCancelled'), tone: 'neutral' },
  };

  /**
   * The payload as label/value pairs. It is the handler's JSON arguments, so
   * an id or an engine handle is the most it says; anything that is not a flat
   * object renders as nothing rather than as a wall of JSON.
   */
  function payloadEntries(raw: string): [string, string][] {
    let parsed: unknown;
    try {
      parsed = JSON.parse(raw || '{}');
    } catch {
      return [];
    }
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return [];
    return Object.entries(parsed).map(([key, value]) => [
      key.replaceAll('_', ' '),
      typeof value === 'string' ? value : JSON.stringify(value),
    ]);
  }

  let meta = $derived(META[job.state]);
  let stamp = $derived(job.updated_at || job.created_at);
  let href = $derived(subjectHref(job.subject_kind, job.subject_id));
  let args = $derived(payloadEntries(job.payload));
  let tries = $derived(t('route.history.detailTriesValue', { count: job.attempts, max: JOB_MAX_ATTEMPTS }));
</script>

<li class="border-b border-border last:border-b-0">
  <button
    type="button"
    class="flex w-full flex-wrap items-center gap-x-3 gap-y-2 px-3 py-3 text-left transition-colors duration-150 ease-out hover:bg-raised"
    aria-expanded={expanded}
    aria-controls={panelID}
    onclick={ontoggle}>
    <span class="size-2 shrink-0 rounded-full {TONE_DOT[meta.tone]}" aria-hidden="true"></span>
    <span class="min-w-36 font-medium text-ink">{jobKindLabel(job.kind)}</span>
    {#if job.subject}
      <span class="min-w-0 max-w-xs truncate text-sm text-ink-secondary" title={job.subject}>{job.subject}</span>
    {/if}
    <Badge tone={meta.tone}>{meta.label}</Badge>
    {#if job.attempts > 0}
      <span class="font-mono text-xs text-ink-secondary">
        {t('route.history.triesFailed', { count: job.attempts, max: JOB_MAX_ATTEMPTS })}
      </span>
    {/if}
    <time class="ml-auto text-sm text-ink-muted" datetime={stamp} title={stamp}>
      {t('route.history.ago', { time: formatAge(stamp) })}
    </time>
    <Icon
      name="chevronDown"
      size={14}
      class="shrink-0 text-ink-muted transition-transform duration-150 ease-out {expanded ? 'rotate-180' : ''}" />
  </button>

  {#if job.state === 'failed' && job.last_error && !expanded}
    <p class="px-3 pb-3 pl-8 text-sm text-danger">{job.last_error}</p>
  {/if}

  {#if expanded}
    <div id={panelID} class="border-t border-border px-3 py-3 pl-8">
      <dl class="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-sm">
        <dt class="text-ink-muted">{t('route.history.detailStatus')}</dt>
        <dd class="text-ink">{meta.label}</dd>

        {#if job.subject}
          <dt class="text-ink-muted">{t('route.history.detailSubject')}</dt>
          <dd class="min-w-0 truncate text-ink">
            {#if href}
              <a href={href} class="text-accent-text hover:underline">{job.subject}</a>
            {:else}
              {job.subject}
            {/if}
          </dd>
        {/if}

        <dt class="text-ink-muted">{t('route.history.detailCreated')}</dt>
        <dd class="text-ink"><time datetime={job.created_at}>{formatDateTime(job.created_at)}</time></dd>

        {#if job.updated_at}
          <dt class="text-ink-muted">{t('route.history.detailUpdated')}</dt>
          <dd class="text-ink"><time datetime={job.updated_at}>{formatDateTime(job.updated_at)}</time></dd>
        {/if}

        {#if job.state === 'pending' && job.run_after}
          <dt class="text-ink-muted">{t('route.history.detailNextTry')}</dt>
          <dd class="text-ink"><time datetime={job.run_after}>{formatDateTime(job.run_after)}</time></dd>
        {/if}

        {#if job.state === 'running' && job.lease_expires_at}
          <dt class="text-ink-muted">{t('route.history.detailLease')}</dt>
          <dd class="text-ink"><time datetime={job.lease_expires_at}>{formatDateTime(job.lease_expires_at)}</time></dd>
        {/if}

        <dt class="text-ink-muted">{t('route.history.detailTries')}</dt>
        <dd class="font-mono text-xs text-ink">{tries}</dd>

        {#if job.last_error}
          <dt class="text-ink-muted">{t('route.history.detailError')}</dt>
          <dd class="whitespace-pre-wrap break-words text-danger">{job.last_error}</dd>
        {/if}

        {#if args.length > 0}
          <dt class="text-ink-muted">{t('route.history.detailArguments')}</dt>
          <dd>
            <ul class="flex flex-col gap-0.5">
              {#each args as [key, value] (key)}
                <li class="font-mono text-xs text-ink-secondary">
                  <span class="text-ink-muted">{key}:</span> {value}
                </li>
              {/each}
            </ul>
          </dd>
        {/if}

        <dt class="text-ink-muted">{t('route.history.detailID')}</dt>
        <dd class="font-mono text-xs text-ink-secondary">#{job.id}</dd>
      </dl>
    </div>
  {/if}
</li>
