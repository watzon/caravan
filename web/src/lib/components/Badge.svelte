<script lang="ts">
  /**
   * DESIGN.md §6: 20px high, radius-sm, mono 12px for quality/codec, Inter for
   * counts. Tint background + coloured text, never solid fills.
   */
  import type { Snippet } from 'svelte';
  import { TONE_TEXT, TONE_TINT, type Tone } from '../status';

  interface Props {
    tone?: Tone;
    /** Machine text (quality, codec, audio) renders in mono — DESIGN.md §2.4. */
    mono?: boolean;
    title?: string;
    class?: string;
    children: Snippet;
  }

  let { tone = 'neutral', mono = false, title, class: klass = '', children }: Props = $props();
</script>

<span
  {title}
  class="inline-flex h-5 shrink-0 items-center rounded-sm px-2 text-xs
         {mono ? 'font-mono font-medium' : 'font-medium'}
         {TONE_TINT[tone]} {TONE_TEXT[tone]} {klass}">
  {@render children()}
</span>
