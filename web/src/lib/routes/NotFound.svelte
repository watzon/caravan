<script lang="ts">
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
</script>

<EmptyState
  icon="search"
  title="No such page"
  message="That URL does not match any screen in Caravan.">
  {#snippet action()}
    <Button variant="primary" href="/movies">Back to the library</Button>
  {/snippet}
</EmptyState>
