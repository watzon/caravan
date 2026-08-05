/**
 * A reactive props bag for mounted-component tests.
 *
 * Svelte 5 only notices a prop change when the value it was handed is reactive,
 * and `$state` is a rune — it may only be written in a `.svelte` or `.svelte.ts`
 * file, which a `.test.ts` is not. So a test that needs to prove a component
 * responds to a value CHANGING from above (rather than to one it was mounted
 * with) reaches for this.
 *
 * Test-only, and deliberately tiny: it is the smallest thing that makes
 * `mount(Component, { props: bag.props })` reactive, with `bag.set` standing in
 * for the screen above.
 */
export interface ReactiveProps<T extends Record<string, unknown>> {
  /** Hand this to `mount`. */
  readonly props: T;
  /** What a parent re-render would do. */
  set(patch: Partial<T>): void;
}

export function reactiveProps<T extends Record<string, unknown>>(initial: T): ReactiveProps<T> {
  const bag = $state({ ...initial });
  return {
    props: bag as T,
    set(patch: Partial<T>) {
      Object.assign(bag, patch);
    },
  };
}
