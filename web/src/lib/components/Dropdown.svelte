<script lang="ts">
  /**
   * A single-choice dropdown: the FilterPill shell with a FilterOptions list
   * inside, for controls that pick one value out of a known set. Sort orders
   * are the case that made it — six rails carried the same native <select>,
   * and the explore filter vocabulary (pill trigger, popover list, tick on
   * the current row) already said everything those selects were saying.
   *
   * The trigger names the current value ("Sort: Title"), the way the select
   * it replaces did, and it wears the accent whenever the value is not the
   * first option: the first option is the default everywhere this is used,
   * so a non-default ordering is an applied state, same as a set filter.
   *
   * A multi-choice control is FilterPill plus FilterOptions by hand; folding
   * both cardinalities into one component is what FilterOptions already does
   * internally, and repeating it here would hide that.
   */
  import FilterOptions from './FilterOptions.svelte';
  import FilterPill from './FilterPill.svelte';

  interface Option {
    id: string;
    name: string;
    hint?: string;
  }

  interface Props {
    /** What the control sorts or chooses, named for the trigger: "{label}: {value}". */
    label: string;
    options: readonly Option[];
    value: string;
    onselect: (id: string) => void;
    /** 'box' for rails of inputs and buttons; the default matches the explore pills. */
    shape?: 'pill' | 'box';
    width?: string;
  }

  let { label, options, value, onselect, shape = 'pill', width = 'w-48' }: Props = $props();

  let current = $derived(options.find((option) => option.id === value));
  let nonDefault = $derived(options.length > 0 && value !== options[0]?.id);
</script>

<FilterPill label="{label}: {current?.name ?? value}" applied={nonDefault} {shape} {width}>
  {#snippet children()}
    <FilterOptions {options} selected={[value]} {onselect} />
  {/snippet}
</FilterPill>
