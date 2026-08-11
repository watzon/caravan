<script lang="ts">
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import { useI18n } from '../i18n.svelte';
  const { t } = useI18n();
</script>

<EmptyState
  icon="search"
  title={t('route.notFound.title')}
  message={t('route.notFound.message')}>
  {#snippet action()}
    <Button variant="primary" href="/movies">{t('route.notFound.backToLibrary')}</Button>
  {/snippet}
</EmptyState>
