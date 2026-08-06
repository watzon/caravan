<script lang="ts">
  /** Label + control + help/error, so every form row is spaced identically. */
  import { setContext } from 'svelte';
  import type { Snippet } from 'svelte';
  import {
    FIELD_ACCESSIBILITY_CONTEXT,
    type FieldAccessibilityContext,
  } from './fieldContext';

  interface Props {
    label: string;
    for?: string;
    help?: string;
    error?: string | null;
    /** Right-aligned on the label row: what this field's current value means. */
    note?: Snippet;
    class?: string;
    children: Snippet;
  }

  let {
    label,
    for: htmlFor,
    help,
    error = null,
    note,
    class: klass = '',
    children,
  }: Props = $props();

  const fieldID = $props.id();
  const helpID = `${fieldID}-help`;
  const errorID = `${fieldID}-error`;

  setContext<FieldAccessibilityContext>(FIELD_ACCESSIBILITY_CONTEXT, {
    get describedBy() {
      return error ? errorID : help ? helpID : undefined;
    },
    get invalid() {
      return Boolean(error);
    },
  });
</script>

<div class="flex flex-col gap-2 {klass}">
  {#if note}
    <div class="flex items-baseline justify-between gap-2">
      {#if htmlFor}
        <label class="micro-label" for={htmlFor}>{label}</label>
      {:else}
        <span class="micro-label">{label}</span>
      {/if}
      {@render note()}
    </div>
  {:else if htmlFor}
    <label class="micro-label" for={htmlFor}>{label}</label>
  {:else}
    <span class="micro-label">{label}</span>
  {/if}
  {@render children()}
  {#if error}
    <p id={errorID} class="text-sm text-danger">{error}</p>
  {:else if help}
    <p id={helpID} class="text-sm text-ink-secondary">{help}</p>
  {/if}
</div>
