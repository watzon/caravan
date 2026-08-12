<script lang="ts">
  interface Props {
    text: string;
  }

  const MAX_TAIL_LENGTH = 28;
  let { text }: Props = $props();

  let split = $derived.by(() => {
    const tailLength = Math.min(MAX_TAIL_LENGTH, Math.ceil(text.length / 2));
    return Math.max(0, text.length - tailLength);
  });
</script>

<!-- CSS can ellipsize only one edge. A shrinkable prefix beside a fixed tail
     puts that edge inside the text and keeps release groups/extensions visible. -->
<span class="flex min-w-0 max-w-full">
  <span class="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">{text.slice(0, split)}</span
  ><span class="shrink-0 whitespace-nowrap">{text.slice(split)}</span>
</span>
