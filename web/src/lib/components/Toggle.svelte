<script lang="ts">
  /** Monitored flags (SPEC §7). Rust when on, because "monitored" is an active state. */
  interface Props {
    checked: boolean;
    label: string;
    /** Hide the visible label but keep it as the accessible name. */
    labelHidden?: boolean;
    disabled?: boolean;
    onchange: (next: boolean) => void;
    class?: string;
  }

  let {
    checked,
    label,
    labelHidden = false,
    disabled = false,
    onchange,
    class: klass = '',
  }: Props = $props();
</script>

<button
  type="button"
  role="switch"
  aria-checked={checked}
  aria-label={labelHidden ? label : undefined}
  {disabled}
  title={labelHidden ? label : undefined}
  onclick={() => onchange(!checked)}
  class="inline-flex items-center gap-3 disabled:opacity-50 {klass}">
  <span
    class="relative inline-flex h-5 w-9 shrink-0 items-center rounded-full border transition-colors duration-150 ease-out
           {checked ? 'border-accent bg-accent' : 'border-border-strong bg-raised'}">
    <span
      class="absolute size-3.5 rounded-full transition-[left] duration-150 ease-out
             {checked ? 'left-[18px] bg-ink-inverse' : 'left-[2px] bg-ink-muted'}">
    </span>
  </span>
  {#if !labelHidden}
    <span class="text-base text-ink-secondary">{label}</span>
  {/if}
</button>
