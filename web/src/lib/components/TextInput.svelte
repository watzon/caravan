<script lang="ts">
  /** DESIGN.md §6: raised fill, strong border, 36px high, rust border on focus. */
  import { getContext } from 'svelte';
  import {
    FIELD_ACCESSIBILITY_CONTEXT,
    type FieldAccessibilityContext,
  } from './fieldContext';

  interface Props {
    value: string;
    id?: string;
    type?: 'text' | 'password' | 'search' | 'date';
    placeholder?: string;
    autofocus?: boolean;
    disabled?: boolean;
    /** Displayed and selectable, but not editable - for generated values. */
    readonly?: boolean;
    mono?: boolean;
    /** Accessible name when this input is not paired to a Field through id/for. */
    ariaLabel?: string;
    autocomplete?: string;
    spellcheck?: boolean;
    oninput?: (event: Event) => void;
    onkeydown?: (event: KeyboardEvent) => void;
    class?: string;
  }

  let {
    value = $bindable(''),
    id,
    type = 'text',
    placeholder,
    autofocus = false,
    disabled = false,
    readonly = false,
    mono = false,
    ariaLabel,
    autocomplete,
    spellcheck,
    oninput,
    onkeydown,
    class: klass = '',
  }: Props = $props();

  const fieldAccessibility = getContext<FieldAccessibilityContext | undefined>(
    FIELD_ACCESSIBILITY_CONTEXT,
  );
</script>

<!-- svelte-ignore a11y_autofocus -->
<input
  {id}
  {type}
  {placeholder}
  {disabled}
  {readonly}
  {autofocus}
  aria-label={ariaLabel}
  autocomplete={autocomplete}
  spellcheck={spellcheck}
  aria-describedby={fieldAccessibility?.describedBy}
  aria-invalid={fieldAccessibility?.invalid ? 'true' : undefined}
  bind:value
  {oninput}
  {onkeydown}
  class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink
         placeholder:text-ink-muted focus:border-accent
         read-only:cursor-text read-only:select-text read-only:border-border read-only:bg-base
         read-only:text-ink-secondary read-only:focus:border-border
         disabled:opacity-50 transition-colors duration-150 ease-out
         {mono ? 'font-mono text-sm' : ''} {klass}" />
