<script lang="ts">
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import { shelfHref } from '../library';
  import { session } from '../state/session.svelte';
  import { useI18n } from '../i18n.svelte';
  const { t } = useI18n();

  let href = $derived(
    session.isAdmin ? shelfHref(session.user?.libraries?.[0]) : '/discover',
  );
</script>

<EmptyState
  icon="search"
  title={t('route.notFound.title')}
  message={t('route.notFound.message')}>
  {#snippet action()}
    <Button variant="primary" {href}>{t('route.notFound.backToLibrary')}</Button>
  {/snippet}
</EmptyState>
