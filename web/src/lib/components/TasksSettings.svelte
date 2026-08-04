<script lang="ts" module>
  /** Fast enough that a task started from this screen visibly starts. */
  export const TASKS_POLL_MS = 5000;
</script>

<script lang="ts">
  /**
   * The recurring background jobs, the way Sonarr's Tasks screen shows them:
   * what runs on a timer, how often, when it last ran and how that went, when
   * the next one is due, and a button that brings it forward.
   *
   * Everything here is read off the job queue itself, so the screen cannot
   * disagree with what the scheduler is actually doing. "Run now" moves the
   * pending row the recurring chain already holds rather than adding one — the
   * chain keeps exactly one open row per task, and a second would double every
   * future cycle.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { SystemTask } from '../api/types';
  import { UNKNOWN, formatAge, formatInterval, formatUntil } from '../format';
  import { pushToast } from '../state/toast.svelte';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import LoadError from './LoadError.svelte';
  import SettingsCard from './SettingsCard.svelte';
  import Skeleton from './Skeleton.svelte';

  let tasks = $state<SystemTask[] | null>(null);
  let error = $state<string | null>(null);
  /** The kinds whose Run now is in flight, so each row's button busies alone. */
  let starting = $state<Record<string, boolean>>({});

  /**
   * A poll that fails leaves the last good list on screen: this pane is a
   * status board, and blanking it because one request in ten timed out is
   * worse than showing a reading a few seconds old. Only the first load turns
   * a failure into an error screen, because then there is nothing to show.
   */
  async function load(initial = false) {
    try {
      tasks = await api.listTasks();
      error = null;
    } catch (err) {
      if (initial || tasks === null) error = errorText(err);
    }
  }

  onMount(() => {
    void load(true);
    const timer = setInterval(() => void load(), TASKS_POLL_MS);
    return () => clearInterval(timer);
  });

  async function run(task: SystemTask) {
    starting = { ...starting, [task.kind]: true };
    try {
      const result = await api.runTask(task.kind);
      if (result?.already_running) {
        pushToast(`${task.name} is already running.`, 'info');
      } else {
        pushToast(`${task.name} will start in a moment.`, 'success');
      }
      // Refresh straight away rather than waiting out the poll: the button was
      // just pressed, so this is the one moment the screen must be current.
      await load();
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      starting = { ...starting, [task.kind]: false };
    }
  }

  /** How long ago the last run finished. "Never" is a real answer here. */
  function lastRunText(task: SystemTask): string {
    if (task.last_result === '') return 'Never';
    const age = formatAge(task.last_run);
    return age === UNKNOWN ? age : `${age} ago`;
  }

  /** When the next run is due, in the same words the queue would use. */
  function nextRunText(task: SystemTask): string {
    if (task.running) return 'Running now';
    if (!task.queued) return 'Not scheduled';
    // An empty next run on a queued task means its run_after is already past —
    // it is waiting on the next poll, not on the clock.
    return task.next_run ? formatUntil(task.next_run) : 'now';
  }
</script>

<SettingsCard
  title="Tasks"
  description="The work Caravan does on a timer. Intervals are set on the panes that own them; anything here can also be run on demand.">
  {#if error}
    <LoadError message={error} onretry={() => load(true)} />
  {:else if tasks === null}
    <div class="flex flex-col gap-4">
      <Skeleton class="h-10 w-full" />
      <Skeleton class="h-10 w-full" />
      <Skeleton class="h-10 w-full" />
    </div>
  {:else}
    <ul class="flex flex-col divide-y divide-border">
      {#each tasks as task (task.kind)}
        <li class="flex flex-col gap-3 py-4 first:pt-0 last:pb-0 lg:flex-row lg:items-start">
          <div class="flex min-w-0 flex-1 flex-col gap-1">
            <div class="flex items-center gap-2">
              <span class="text-base font-medium text-ink">{task.name}</span>
              {#if task.running}
                <Badge tone="accent">Running</Badge>
              {/if}
            </div>
            <p class="text-sm text-ink-secondary">{task.description}</p>
            <!-- The reason a failure happened, under the row that failed:
                 "last run failed" with no "why" only sends people to the logs. -->
            {#if task.last_result === 'failed' && task.last_error}
              <p class="text-sm text-danger" title={task.last_error}>{task.last_error}</p>
            {/if}
          </div>

          <dl class="grid shrink-0 grid-cols-3 gap-4 lg:w-72">
            <div>
              <dt class="micro-label">Every</dt>
              <dd class="mt-1 text-sm tabular-nums text-ink">
                {formatInterval(task.interval_minutes)}
              </dd>
            </div>
            <div>
              <dt class="micro-label">Last run</dt>
              <dd class="mt-1 flex flex-wrap items-center gap-1.5 text-sm tabular-nums text-ink">
                <span class={task.last_result === '' ? 'text-ink-secondary' : ''}>
                  {lastRunText(task)}
                </span>
                {#if task.last_result !== ''}
                  <Badge tone={task.last_result === 'ok' ? 'success' : 'danger'}>
                    {task.last_result === 'ok' ? 'OK' : 'Failed'}
                  </Badge>
                {/if}
              </dd>
            </div>
            <div>
              <dt class="micro-label">Next run</dt>
              <dd class="mt-1 text-sm tabular-nums text-ink">{nextRunText(task)}</dd>
            </div>
          </dl>

          <Button
            class="shrink-0 lg:ml-4"
            disabled={starting[task.kind]}
            onclick={() => run(task)}>
            <Icon name="refresh" size={14} />
            {starting[task.kind] ? 'Starting…' : 'Run now'}
          </Button>
        </li>
      {/each}
    </ul>
  {/if}
</SettingsCard>
