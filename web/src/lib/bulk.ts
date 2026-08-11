import { translate } from './i18n.svelte';

/**
 * Running one per-item endpoint over a selection.
 *
 * There is no bulk endpoint on the server (SPEC §11), so a bulk action is a
 * loop over the per-item ones. It runs sequentially: a selection of a hundred
 * items firing a hundred concurrent searches would be a self-inflicted burst
 * on the indexers, and the bar reports a count either way.
 *
 * A failure is counted, never thrown: eighteen of twenty removals succeeding is
 * a result the user has to be told, not an exception that hides it.
 */
export interface BulkResult {
  ok: number;
  failed: number;
  total: number;
}

export async function runBulk(
  ids: readonly number[],
  action: (id: number) => Promise<unknown>,
): Promise<BulkResult> {
  let ok = 0;
  let failed = 0;
  for (const id of ids) {
    try {
      await action(id);
      ok++;
    } catch {
      failed++;
    }
  }
  return { ok, failed, total: ids.length };
}

/** "Monitored 5", or "Monitored 4 of 5" when some failed. */
export function bulkSummary(result: BulkResult, verb: string): string {
  return result.failed === 0
    ? translate('bulk.complete', { verb, total: result.total })
    : translate('bulk.partial', { verb, ok: result.ok, total: result.total });
}
