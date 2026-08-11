<script lang="ts">
  import { useI18n, type TranslationKey, type TranslationValues } from '../i18n.svelte';

  interface Link {
    href: string;
    label: TranslationKey;
    class?: string;
  }

  interface Props {
    message: TranslationKey;
    values?: TranslationValues;
    links: Record<string, Link>;
  }

  let { message, values, links }: Props = $props();
  const { parts, t } = useI18n();
  let messageParts = $derived(parts(message, values));
</script>

{#each messageParts as part, index (index)}
  {#if typeof part === 'string'}
    {part}
  {:else if links[part.placeholder]}
    {@const link = links[part.placeholder]}
    <a href={link.href} class={link.class}>{t(link.label)}</a>
  {:else}
    {`{${part.placeholder}}`}
  {/if}
{/each}
