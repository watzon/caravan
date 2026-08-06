<script lang="ts">
  /** DESIGN.md §6: tint background + semantic left bar; banners do not auto-dismiss. */
  import type { Snippet } from 'svelte';
  import { TONE_DOT, TONE_TEXT, TONE_TINT, type Tone } from '../status';
  import Icon, { type IconName } from './Icon.svelte';

  interface Props {
    tone?: Tone;
    icon?: IconName;
    title?: string;
    message: string;
    action?: Snippet;
    class?: string;
  }

  let { tone = 'info', icon = 'warning', title, message, action, class: klass = '' }: Props =
    $props();
</script>

<div class="flex items-start gap-3 overflow-hidden rounded-md {TONE_TINT[tone]} {klass}" role="alert">
  <span class="w-0.5 self-stretch {TONE_DOT[tone]}"></span>
  <span class="pt-3 {TONE_TEXT[tone]}"><Icon name={icon} /></span>
  <div class="min-w-0 flex-1 py-3 pr-3">
    {#if title}
      <p class="break-words text-base font-semibold text-ink">{title}</p>
    {/if}
    <p class="break-words text-base text-ink-secondary">{message}</p>
  </div>
  {#if action}
    <div class="flex shrink-0 items-center self-stretch py-3 pr-3">{@render action()}</div>
  {/if}
</div>
