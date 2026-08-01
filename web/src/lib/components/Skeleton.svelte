<script lang="ts">
  /** Loading placeholder. Pulse only — DESIGN.md §7 bans entrance choreography. */
  interface Props {
    class?: string;
  }

  let { class: klass = 'h-4 w-full' }: Props = $props();
</script>

<div class="animate-pulse rounded-sm bg-raised {klass}" aria-hidden="true"></div>
