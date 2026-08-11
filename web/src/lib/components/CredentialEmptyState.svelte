<script lang="ts">
  /**
   * What a metadata-needing surface renders when the TMDB key is missing or
   * rejected (PLAN phase 10 task 3).
   *
   * It is an empty state rather than an error, because that is what it is: the
   * screen has nothing to show and there is exactly one thing that would change
   * that. A retry would not, so it is not offered — the destination is.
   *
   * The destination is offered only to somebody who can walk through it.
   * `/settings/:section` is not a member route (router.ts), and the guard in
   * App.svelte replace-navigates a member back to /discover — so on Discover,
   * DiscoverBrowse and DiscoverTitle, all member screens, the button used to
   * bounce the reader straight back to the broken screen they clicked it from.
   * A member gets the copy that names who can fix it, and no dead door.
   */
  import { metadataCopy, type CredentialFault } from '../credentials';
  import { session } from '../state/session.svelte';
  import { useI18n } from '../i18n.svelte';
  import Button from './Button.svelte';
  import EmptyState from './EmptyState.svelte';

  interface Props {
    fault: CredentialFault;
    class?: string;
  }

  let { fault, class: klass = '' }: Props = $props();
  const { t } = useI18n();

  let canFix = $derived(session.isAdmin);
  let copy = $derived(metadataCopy(fault, canFix));
</script>

<EmptyState icon="settings" title={copy.title} message={copy.message} class={klass}>
  {#snippet action()}
    {#if canFix}
      <Button variant="primary" href="/settings/metadata">{t('component.credentials.openSettings')}</Button>
    {/if}
  {/snippet}
</EmptyState>
