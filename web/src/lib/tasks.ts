/**
 * Labels and summaries for the job queue, as the sidebar footer and the
 * Settings badge read them. Pure — unit-tested in tasks.test.ts.
 *
 * Recurring tasks (RSS, backlog) report `running` on GET /system/tasks.
 * One-shot work (a scene search, an import) lives on GET /jobs as pending or
 * running rows. Housekeeping kinds run constantly and are not news.
 */

import type { DownloadStatus, Job, SystemTask } from './api/types';
import { translate, translatePlural } from './i18n.svelte';

const HOUSEKEEPING = new Set([
  'notification_dispatch',
  'indexer_health',
  'recycle_cleanup',
]);

const ONESHOT = new Set([
  'search_movie',
  'search_episode',
  'import',
  'sync_site',
  'move_item',
]);

export interface TaskActivity {
  /** Stable key for the stack row. */
  id: string;
  /** Short line for the footer. */
  label: string;
  /** Longer title attribute. */
  title: string;
  href: string;
  /** True while work is happening right now. */
  spinning: boolean;
  tone: 'accent' | 'warning';
}

export interface FooterStackInput {
  tasks: readonly SystemTask[] | null;
  jobs: readonly Job[] | null;
  downloads?: readonly DownloadStatus[] | null;
  converting?: number;
}

/** How many rows the sidebar rail will show before collapsing the rest. */
export const FOOTER_STACK_LIMIT = 6;

export function isHousekeepingKind(kind: string): boolean {
  return HOUSEKEEPING.has(kind);
}

export function isOneshotKind(kind: string): boolean {
  return ONESHOT.has(kind);
}

export function jobKindLabel(kind: string): string {
  switch (kind) {
    case 'rss_sync':
      return translate('task.kind.rssSync');
    case 'backlog_sweep':
      return translate('task.kind.backlogSweep');
    case 'search_movie':
      return translate('task.kind.searchMovie');
    case 'search_episode':
      return translate('task.kind.searchEpisode');
    case 'refresh_metadata':
      return translate('task.kind.refreshMetadata');
    case 'recycle_cleanup':
      return translate('task.kind.recycleCleanup');
    case 'notification_dispatch':
      return translate('task.kind.notificationDispatch');
    case 'indexer_health':
      return translate('task.kind.indexerHealth');
    case 'sync_site':
      return translate('task.kind.syncSite');
    case 'import':
      return translate('task.kind.import');
    case 'move_item':
      return translate('task.kind.moveItem');
    default:
      return kind.replaceAll('_', ' ');
  }
}

/** Recurring tasks whose last finished run failed. */
export function failedTaskCount(tasks: readonly SystemTask[] | null): number {
  return (tasks ?? []).filter((task) => task.last_result === 'failed').length;
}

function liveOneshots(jobs: readonly Job[]): Job[] {
  return jobs.filter(
    (job) =>
      isOneshotKind(job.kind) && (job.state === 'running' || job.state === 'pending'),
  );
}

function runningRecurring(tasks: readonly SystemTask[]): SystemTask[] {
  return tasks.filter((task) => task.running && !isHousekeepingKind(task.kind));
}

/**
 * The rows the footer should show at once. Searches group by the title they
 * are for so two shows do not collapse into "Searching 5". Queued downloads
 * and conversions sit in the same stack — they are work the user started,
 * not only the recurring task list.
 */
export function footerStack(input: FooterStackInput): TaskActivity[] {
  const rows: TaskActivity[] = [];
  const oneshots = liveOneshots(input.jobs ?? []);

  for (const group of groupSearches(oneshots)) {
    rows.push(searchRow(group));
  }

  const queued = (input.downloads ?? []).filter((item) => item.state === 'queued');
  if (queued.length === 1) {
    const item = queued[0]!;
    rows.push({
      id: `queue:${item.id}`,
      label: translatePlural('task.footer.queuing', 1, { name: item.name }),
      title: translatePlural('task.footer.queuingTitle', 1),
      href: '/queue',
      spinning: true,
      tone: 'accent',
    });
  } else if (queued.length > 1) {
    rows.push({
      id: 'queue',
      label: translatePlural('task.footer.queuing', queued.length),
      title: translatePlural('task.footer.queuingTitle', queued.length),
      href: '/queue',
      spinning: true,
      tone: 'accent',
    });
  }

  const converting = input.converting ?? 0;
  if (converting > 0) {
    rows.push({
      id: 'convert',
      label: translatePlural('task.footer.converting', converting),
      title: translatePlural('task.footer.convertingTitle', converting),
      href: '/convert',
      spinning: true,
      tone: 'accent',
    });
  }

  const imports = oneshots.filter((job) => job.kind === 'import');
  if (imports.length > 0) {
    rows.push({
      id: 'import',
      label: translate('task.footer.importing'),
      title: translatePlural('task.footer.importingTitle', imports.length),
      href: '/history',
      spinning: true,
      tone: 'accent',
    });
  }

  for (const group of groupCatalogues(oneshots)) {
    rows.push({
      id: `sync:${group.key}`,
      label: group.subject
        ? translate('task.footer.cataloguingNamed', { name: group.subject })
        : translatePlural('task.footer.cataloguing', group.count),
      title: translatePlural('task.footer.cataloguingTitle', group.count),
      href: subjectHref(group.kind, group.subjectId) ?? '/adult',
      spinning: true,
      tone: 'accent',
    });
  }

  const moves = oneshots.filter((job) => job.kind === 'move_item');
  if (moves.length === 1) {
    const move = moves[0]!;
    rows.push({
      id: 'move',
      label: translate('task.footer.moving'),
      title: translatePlural('task.footer.movingTitle', 1),
      href: subjectHref(move.subject_kind, move.subject_id) ?? '/history',
      spinning: true,
      tone: 'accent',
    });
  } else if (moves.length > 1) {
    rows.push({
      id: 'move',
      label: translate('task.footer.moving'),
      title: translatePlural('task.footer.movingTitle', moves.length),
      href: '/history',
      spinning: true,
      tone: 'accent',
    });
  }

  const recurring = runningRecurring(input.tasks ?? []);
  if (recurring.length === 1) {
    const task = recurring[0]!;
    rows.push({
      id: `task:${task.kind}`,
      label: task.name,
      title: task.description || task.name,
      href: '/settings/tasks',
      spinning: true,
      tone: 'accent',
    });
  } else if (recurring.length > 1) {
    rows.push({
      id: 'tasks',
      label: translatePlural('task.footer.running', recurring.length),
      title: recurring.map((task) => task.name).join(', '),
      href: '/settings/tasks',
      spinning: true,
      tone: 'accent',
    });
  }

  const failed = (input.tasks ?? []).filter((task) => task.last_result === 'failed');
  if (failed.length === 1) {
    const task = failed[0]!;
    rows.push({
      id: `failed:${task.kind}`,
      label: translate('task.footer.failed', { name: task.name }),
      title: task.last_error || task.name,
      href: '/settings/tasks',
      spinning: false,
      tone: 'warning',
    });
  } else if (failed.length > 1) {
    rows.push({
      id: 'failed',
      label: translatePlural('task.footer.failedMany', failed.length),
      title: failed.map((task) => task.name).join(', '),
      href: '/settings/tasks',
      spinning: false,
      tone: 'warning',
    });
  }

  if (rows.length <= FOOTER_STACK_LIMIT) return rows;
  const visible = rows.slice(0, FOOTER_STACK_LIMIT - 1);
  const extra = rows.length - visible.length;
  visible.push({
    id: 'more',
    label: translate('task.footer.more', { count: extra }),
    title: rows
      .slice(FOOTER_STACK_LIMIT - 1)
      .map((row) => row.label)
      .join(', '),
    href: rows[FOOTER_STACK_LIMIT - 1]?.href ?? '/history',
    spinning: rows.slice(FOOTER_STACK_LIMIT - 1).some((row) => row.spinning),
    tone: 'accent',
  });
  return visible;
}

/** First stack row, for callers that still want a single line. */
export function footerActivity(
  tasks: readonly SystemTask[] | null,
  jobs: readonly Job[] | null,
): TaskActivity | null {
  return footerStack({ tasks, jobs })[0] ?? null;
}

interface NamedGroup {
  key: string;
  subject: string;
  kind: 'movie' | 'series' | 'site' | 'unknown';
  subjectId?: number;
  count: number;
}

function subjectHref(kind: string | undefined, id: number | undefined): string | null {
  if (!id) return null;
  if (kind === 'movie') return `/movies/${id}`;
  if (kind === 'series') return `/series/${id}`;
  if (kind === 'site') return `/adult/sites/${id}`;
  return null;
}

function groupSearches(jobs: readonly Job[]): NamedGroup[] {
  return groupBySubject(
    jobs.filter((job) => job.kind === 'search_movie' || job.kind === 'search_episode'),
    (job) => {
      if (job.subject_kind === 'site' || job.subject_kind === 'series' || job.subject_kind === 'movie') {
        return job.subject_kind;
      }
      return job.kind === 'search_movie' ? 'movie' : 'series';
    },
  );
}

function groupCatalogues(jobs: readonly Job[]): NamedGroup[] {
  return groupBySubject(
    jobs.filter((job) => job.kind === 'sync_site'),
    () => 'site',
  );
}

function groupBySubject(
  jobs: readonly Job[],
  kindOf: (job: Job) => NamedGroup['kind'],
): NamedGroup[] {
  const groups: NamedGroup[] = [];
  const index = new Map<string, NamedGroup>();
  for (const job of jobs) {
    const kind = kindOf(job);
    const subject = job.subject?.trim() ?? '';
    const subjectId = job.subject_id && job.subject_id > 0 ? job.subject_id : undefined;
    const key = subjectId ? `${kind}:${subjectId}` : subject ? `${kind}:${subject.toLowerCase()}` : `${kind}:?`;
    const existing = index.get(key);
    if (existing) {
      existing.count += 1;
      continue;
    }
    const group = { key, subject, kind, subjectId, count: 1 };
    index.set(key, group);
    groups.push(group);
  }
  return groups;
}

function searchHref(group: NamedGroup): string {
  return subjectHref(group.kind, group.subjectId) ?? (group.kind === 'site' ? '/adult' : '/wanted');
}

function searchRow(group: NamedGroup): TaskActivity {
  const name = group.subject;
  const href = searchHref(group);
  if (group.kind === 'site' && name) {
    return {
      id: `search:${group.key}`,
      label: translatePlural('task.footer.searchingScenes', group.count, { name }),
      title: translatePlural('task.footer.searchingTitle', group.count),
      href,
      spinning: true,
      tone: 'accent',
    };
  }
  if (group.kind === 'series' && name) {
    return {
      id: `search:${group.key}`,
      label: translatePlural('task.footer.searchingEpisodes', group.count, { name }),
      title: translatePlural('task.footer.searchingTitle', group.count),
      href,
      spinning: true,
      tone: 'accent',
    };
  }
  if (group.kind === 'movie' && name) {
    return {
      id: `search:${group.key}`,
      label:
        group.count === 1
          ? translate('task.footer.searchingNamed', { name })
          : translatePlural('task.footer.searchingMovies', group.count, { name }),
      title: translatePlural('task.footer.searchingTitle', group.count),
      href,
      spinning: true,
      tone: 'accent',
    };
  }
  return {
    id: `search:${group.key}`,
    label: translatePlural('task.footer.searching', group.count),
    title: translatePlural('task.footer.searchingTitle', group.count),
    href,
    spinning: true,
    tone: 'accent',
  };
}
