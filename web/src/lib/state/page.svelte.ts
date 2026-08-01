/**
 * Per-page header state: how a route talks to the shared TopBar layout.
 *
 * The TopBar owns the page title (from the route table) and the global
 * affordances (search, health). A route that has more to say in the header
 * registers it here from an $effect, and clears it in the effect's cleanup
 * so the next page starts empty:
 *
 *   $effect(() => {
 *     page.subtitle = `${active} active`;
 *     return () => (page.subtitle = null);
 *   });
 */
import type { Snippet } from 'svelte';

interface PageHeader {
  subtitle: string | null;
  actions: Snippet | null;
}

export const page = $state<PageHeader>({ subtitle: null, actions: null });
