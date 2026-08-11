<script lang="ts">
  /** Uniform failure state for every fetch in the app (SPEC §13: never silent). */
  import Banner from './Banner.svelte';
  import { useI18n } from '../i18n.svelte';
  import Button from './Button.svelte';

  interface Props {
    message: string;
    onretry?: () => void;
    class?: string;
  }

  let { message, onretry, class: klass = '' }: Props = $props();
  const { t } = useI18n();
</script>

<Banner tone="danger" icon="warning" title={t('component.error.title')} {message} class={klass}>
  {#snippet action()}
    {#if onretry}
      <Button variant="secondary" size="sm" onclick={onretry}>{t('component.actions.retry')}</Button>
    {/if}
  {/snippet}
</Banner>
