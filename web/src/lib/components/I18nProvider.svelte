<script lang="ts">
  import { untrack, type Snippet } from 'svelte';
  import { currentLocale, provideI18n, type Locale } from '../i18n.svelte';

  interface Props {
    children: Snippet;
    locale?: Locale;
  }

  let { children, locale = 'en' }: Props = $props();
  const i18n = provideI18n(untrack(() => locale));

  $effect(() => {
    i18n.setLocale(locale);
    document.documentElement.lang = currentLocale();
  });
</script>

{@render children()}
