<script lang="ts">
  /** DESIGN.md §6: raised fill, strong border, 36px high, rust border on focus. */
  interface Props {
    value: string;
    id?: string;
    type?: 'text' | 'password' | 'search';
    placeholder?: string;
    autofocus?: boolean;
    disabled?: boolean;
    mono?: boolean;
    ariaLabel?: string;
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
    mono = false,
    ariaLabel,
    oninput,
    onkeydown,
    class: klass = '',
  }: Props = $props();
</script>

<!-- svelte-ignore a11y_autofocus -->
<input
  {id}
  {type}
  {placeholder}
  {disabled}
  {autofocus}
  aria-label={ariaLabel}
  bind:value
  {oninput}
  {onkeydown}
  class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink
         placeholder:text-ink-muted focus:border-accent focus:outline-none
         disabled:opacity-50 transition-colors duration-150 ease-out
         {mono ? 'font-mono text-sm' : ''} {klass}" />
