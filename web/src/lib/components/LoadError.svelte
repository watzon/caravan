<script lang="ts">
  /** Uniform failure state for every fetch in the app (SPEC §13: never silent). */
  import Banner from './Banner.svelte';
  import Button from './Button.svelte';

  interface Props {
    message: string;
    onretry?: () => void;
    class?: string;
  }

  let { message, onretry, class: klass = '' }: Props = $props();
</script>

<Banner tone="danger" icon="warning" title="Something went wrong" {message} class={klass}>
  {#snippet action()}
    {#if onretry}
      <Button variant="secondary" size="sm" onclick={onretry}>Retry</Button>
    {/if}
  {/snippet}
</Banner>
