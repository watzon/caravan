/**
 * Live invalidation for the sidebar stores.
 *
 * GET /api/v1/events/stream sends a resource name; the matching store
 * re-reads its REST snapshot. The snapshot remains the source of truth.
 * Local mutations still write the stores directly; this is how another
 * browser, or a background job, updates the same badges.
 */

import { downloads } from './downloads.svelte';
import { requests } from './requests.svelte';
import { session } from './session.svelte';
import { system } from './system.svelte';
import { tasks } from './tasks.svelte';

const STREAM = '/api/v1/events/stream';

export function applyInvalidation(resource: string): void {
  switch (resource) {
    case 'library':
      void system.refresh();
      // The identity as well as the counts: since the sidebar's shelf rows are
      // built from /auth/me, a library renamed, re-iconed, switched on or
      // created in ANOTHER browser has to reach this one's navigation, not just
      // its badges. `library` is the resource every one of those writes emits.
      void session.refresh();
      return;
    case 'requests':
      void requests.refresh();
      return;
    case 'downloads':
      void downloads.refresh();
      return;
    case 'jobs':
      void tasks.refresh();
      return;
  }
}

/** Re-read every sidebar store. Used when a tab becomes visible again. */
export function refreshSidebarStores(): void {
  void system.refresh();
  void requests.refresh();
  void downloads.refresh();
  void tasks.refresh();
}

export function startLiveUpdates(): () => void {
  if (typeof EventSource !== 'function' || typeof document === 'undefined') {
    return () => {};
  }

  let source: EventSource | null = null;
  let stopped = false;
  let attempt = 0;
  let opened = false;
  let reconnect: ReturnType<typeof setTimeout> | null = null;

  const connect = () => {
    if (stopped) return;
    source = new EventSource(STREAM);
    source.addEventListener('invalidate', (event) => {
      attempt = 0;
      try {
        const body = JSON.parse((event as MessageEvent).data) as { resource?: string };
        if (body.resource) applyInvalidation(body.resource);
      } catch {
        // A malformed hint is dropped; the next one or a visibility refresh
        // will load the snapshot.
      }
    });
    source.onopen = () => {
      if (opened) refreshSidebarStores();
      opened = true;
      attempt = 0;
    };
    source.onerror = () => {
      source?.close();
      source = null;
      if (stopped) return;
      const delay = Math.min(30_000, 250 * 2 ** attempt) * (0.5 + Math.random());
      attempt += 1;
      reconnect = setTimeout(connect, delay);
    };
  };

  const onVisible = () => {
    if (!document.hidden) refreshSidebarStores();
  };

  connect();
  document.addEventListener('visibilitychange', onVisible);

  return () => {
    stopped = true;
    if (reconnect !== null) clearTimeout(reconnect);
    source?.close();
    document.removeEventListener('visibilitychange', onVisible);
  };
}
