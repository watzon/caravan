<script lang="ts">
  /**
   * The scope row under Explore's title: Featured · Movies · Series · Adult.
   *
   * Anchors rather than buttons, and paths rather than in-page state, because
   * each scope is a screen somebody links to. The Adult pill is ABSENT for a
   * reader without the grant, not disabled — the module is invisible, not
   * switched off (SPEC §12), and a greyed-out pill announces that it exists.
   *
   * The grant is read here through `session.adult`, which is the server's
   * already-ANDed answer; the route itself is gated a second time in App.svelte
   * (isAdultRoute), so a hidden pill is a courtesy and not the enforcement.
   */
  import { exploreScopeHref, visibleScopes, type ExploreScope } from '../explore';
  import { session } from '../state/session.svelte';

  interface Props {
    active: ExploreScope;
    /** "24,861 movies match" — the line under the row; "" hides it. */
    note?: string;
  }

  let { active, note = '' }: Props = $props();

  let scopes = $derived(visibleScopes(session.adult));
</script>

<div class="flex flex-col gap-2">
  <nav class="flex flex-wrap gap-2" aria-label="Explore scopes">
    {#each scopes as scope (scope.key)}
      <a
        href={exploreScopeHref(scope.key)}
        aria-current={active === scope.key ? 'page' : undefined}
        class="inline-flex h-8 items-center rounded-full border px-4 text-base
               transition-colors duration-150 ease-out
               {active === scope.key
          ? 'border-accent bg-accent-tint text-accent-text'
          : 'border-border bg-surface text-ink-secondary hover:border-border-strong hover:bg-raised hover:text-ink'}">
        {scope.label}
      </a>
    {/each}
  </nav>

  {#if note}
    <p class="text-sm text-ink-secondary">{note}</p>
  {/if}
</div>
