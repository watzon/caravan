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
  import type { SystemTask, TaskIntervalUpdate } from '../api/types';
  import { UNKNOWN, formatAge, formatUntil } from '../format';
  import { tasks as taskActivity } from '../state/tasks.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { useI18n } from '../i18n.svelte';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import LoadError from './LoadError.svelte';
  import SettingsCard from './SettingsCard.svelte';
  import Skeleton from './Skeleton.svelte';

  const { t, tp } = useI18n();

  let tasks = $state<SystemTask[] | null>(null);
  let error = $state<string | null>(null);
  /** The kinds whose Run now is in flight, so each row's button busies alone. */
  let starting = $state<Record<string, boolean>>({});
  /** Draft interval text exists only after a user edits that task. */
  let intervalDrafts = $state<Record<string, string>>({});
  /** The kinds whose interval update is in flight. */
  let updating = $state<Record<string, boolean>>({});

  /**
   * A poll that fails leaves the last good list on screen: this pane is a
   * status board, and blanking it because one request in ten timed out is
   * worse than showing a reading a few seconds old. Only the first load turns
   * a failure into an error screen, because then there is nothing to show.
   */
  async function load(initial = false, confirmedInterval?: TaskIntervalUpdate) {
    try {
      const latest = await api.listTasks();
      // A successful PUT response is authoritative for that task even if a
      // concurrent list read was served from an older scheduler snapshot.
      tasks = confirmedInterval
        ? latest.map((task) =>
            task.kind === confirmedInterval.kind
              ? { ...task, interval_minutes: confirmedInterval.interval_minutes }
              : task,
          )
        : latest;
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
        pushToast(t('component.tasksSettings.alreadyRunning', { name: task.name }), 'info');
      }
      // The sidebar footer is the progress surface. Watch immediately so a
      // run that just left this button is visible there on the next tick.
      taskActivity.watchSoon();
      // Refresh straight away rather than waiting out the poll: the button was
      // just pressed, so this is the one moment the screen must be current.
      await load();
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      starting = { ...starting, [task.kind]: false };
    }
  }

  function intervalValue(task: SystemTask): string {
    return intervalDrafts[task.kind] ?? String(task.interval_minutes);
  }

  function setIntervalValue(task: SystemTask, event: Event) {
    intervalDrafts = {
      ...intervalDrafts,
      [task.kind]: (event.currentTarget as HTMLInputElement).value,
    };
  }

  function intervalIssue(task: SystemTask): string | null {
    const value = intervalValue(task).trim();
    const minutes = Number(value);
    if (!value || !Number.isInteger(minutes) || minutes < 5 || minutes > 43_200) {
      return t('component.tasksSettings.intervalError');
    }
    return null;
  }

  function intervalChanged(task: SystemTask): boolean {
    const issue = intervalIssue(task);
    return issue === null && Number(intervalValue(task)) !== task.interval_minutes;
  }

  async function saveInterval(task: SystemTask) {
    const issue = intervalIssue(task);
    if (updating[task.kind] || !intervalChanged(task) || issue !== null) return;

    updating = { ...updating, [task.kind]: true };
    try {
      const result = await api.updateTaskInterval(task.kind, {
        interval_minutes: Number(intervalValue(task)),
      });
      const nextDrafts = { ...intervalDrafts };
      delete nextDrafts[task.kind];
      intervalDrafts = nextDrafts;
      pushToast(t('component.tasksSettings.intervalSaved', { name: task.name }), 'success');
      // Unlike ordinary polling, this request follows a user action: refresh
      // now so the scheduler state and displayed cadence agree immediately.
      await load(false, result);
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      updating = { ...updating, [task.kind]: false };
    }
  }

  /** How long ago the last run finished. "Never" is a real answer here. */
  function lastRunText(task: SystemTask): string {
    if (task.last_result === '') return t('component.tasksSettings.never');
    const age = formatAge(task.last_run);
    return age === UNKNOWN ? age : t('component.tasksSettings.ago', { age });
  }

  /** When the next run is due, in the same words the queue would use. */
  function nextRunText(task: SystemTask): string {
    if (task.running) return t('component.tasksSettings.runningNow');
    if (!task.queued) return t('component.tasksSettings.notScheduled');
    // An empty next run on a queued task means its run_after is already past —
    // it is waiting on the next poll, not on the clock.
    return task.next_run ? formatUntil(task.next_run) : t('component.tasksSettings.now');
  }
</script>

<SettingsCard
  title={t('component.tasksSettings.tasks')}
  description={t('component.tasksSettings.theWorkCaravanDoesOnATimerSetEachRecurringIntervalInMinutesOrRunATaskOnDemand')}>
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
                <Badge tone="accent">{t('component.tasksSettings.running')}</Badge>
              {/if}
            </div>
            <p class="text-sm text-ink-secondary">{task.description}</p>
            <!-- The reason a failure happened, under the row that failed:
                 "last run failed" with no "why" only sends people to the logs. -->
            {#if task.last_result === 'failed' && task.last_error}
              <p class="text-sm text-danger" title={task.last_error}>{task.last_error}</p>
            {/if}
          </div>

          <dl class="grid shrink-0 grid-cols-1 gap-4 sm:grid-cols-3 lg:w-80">
            <div>
              <dt class="micro-label">
                <label for={`task-${task.kind}-interval`}>{t('component.tasksSettings.everyMinutes')}</label>
              </dt>
              <dd class="mt-1">
                <input
                  id={`task-${task.kind}-interval`}
                  type="number"
                  min="5"
                  max="43200"
                  step="1"
                  value={intervalValue(task)}
                  aria-invalid={intervalIssue(task) ? 'true' : undefined}
                  aria-describedby={intervalIssue(task) ? `task-${task.kind}-interval-error` : undefined}
                  oninput={(event) => setIntervalValue(task, event)}
                  class="h-9 w-full min-w-0 rounded-sm border border-border-strong bg-raised px-2 font-mono text-sm tabular-nums text-ink
                         focus:border-accent focus:outline-none disabled:opacity-50 transition-colors duration-150 ease-out"
                  disabled={updating[task.kind]} />
                {#if intervalIssue(task)}
                  <p
                    id={`task-${task.kind}-interval-error`}
                    class="mt-1 text-sm text-danger"
                    role="alert">
                    {intervalIssue(task)}
                  </p>
                {/if}
              </dd>
            </div>
            <div>
              <dt class="micro-label">{t('component.tasksSettings.lastRun')}</dt>
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
              <dt class="micro-label">{t('component.tasksSettings.nextRun')}</dt>
              <dd class="mt-1 text-sm tabular-nums text-ink">{nextRunText(task)}</dd>
            </div>
          </dl>

          <div class="flex shrink-0 flex-wrap items-center gap-2 lg:ml-4">
            <Button
              variant="secondary"
              disabled={updating[task.kind] || !intervalChanged(task)}
              title={!intervalChanged(task)
                ? intervalIssue(task) ?? t('component.tasksSettings.noChangesToSave')
                : undefined}
              onclick={() => saveInterval(task)}>
              {updating[task.kind] ? t('component.tasksSettings.saving') : t('component.tasksSettings.saveInterval')}
            </Button>
            <Button
              disabled={starting[task.kind]}
              onclick={() => run(task)}>
              <Icon name="refresh" size={14} />
              {starting[task.kind] ? t('component.tasksSettings.starting') : t('component.tasksSettings.runNow')}
            </Button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</SettingsCard>
