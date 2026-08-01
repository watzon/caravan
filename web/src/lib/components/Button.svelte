<script lang="ts">
  /** DESIGN.md §6: primary (rust fill, dark text), secondary, ghost, danger. */
  import type { Snippet } from 'svelte';

  interface Props {
    variant?: 'primary' | 'secondary' | 'ghost' | 'danger';
    size?: 'sm' | 'md';
    type?: 'button' | 'submit';
    href?: string;
    disabled?: boolean;
    title?: string;
    onclick?: (event: MouseEvent) => void;
    class?: string;
    children: Snippet;
  }

  let {
    variant = 'secondary',
    size = 'md',
    type = 'button',
    href,
    disabled = false,
    title,
    onclick,
    class: klass = '',
    children,
  }: Props = $props();

  const BASE =
    'inline-flex items-center justify-center gap-2 rounded-md font-medium whitespace-nowrap transition-colors duration-150 ease-out disabled:opacity-50 disabled:pointer-events-none';

  const SIZES = {
    sm: 'h-7 px-2 text-sm',
    md: 'h-8 px-3 text-base',
  } as const;

  const VARIANTS = {
    primary: 'bg-accent text-ink-inverse hover:bg-accent-hover',
    secondary: 'bg-raised text-ink border border-border-strong hover:bg-overlay',
    ghost: 'text-ink-secondary hover:text-ink hover:bg-raised',
    danger: 'bg-danger text-ink hover:brightness-110',
  } as const;

  let classes = $derived(`${BASE} ${SIZES[size]} ${VARIANTS[variant]} ${klass}`);
</script>

{#if href}
  <a class={classes} {href} {title} aria-disabled={disabled} {onclick}>{@render children()}</a>
{:else}
  <button class={classes} {type} {disabled} {title} {onclick}>{@render children()}</button>
{/if}
