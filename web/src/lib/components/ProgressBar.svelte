<script lang="ts">
  /** DESIGN.md §6: 4px track on --color-border, semantic fill, width-only motion. */
  import { TONE_DOT, type Tone } from '../status';

  interface Props {
    /** Fraction in [0,1]. */
    value: number;
    tone?: Tone;
    label?: string;
    class?: string;
  }

  let { value, tone = 'accent', label, class: klass = '' }: Props = $props();
  let pct = $derived(Math.round(Math.max(0, Math.min(1, value)) * 100));
</script>

<div
  class="h-1 w-full overflow-hidden rounded-full bg-border {klass}"
  role="progressbar"
  aria-valuenow={pct}
  aria-valuemin="0"
  aria-valuemax="100"
  aria-label={label}>
  <div
    class="h-full rounded-full transition-[width] duration-150 ease-out {TONE_DOT[tone]}"
    style="width: {pct}%">
  </div>
</div>
