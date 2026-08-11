<script lang="ts">
  import { useI18n } from '../i18n.svelte';
  /**
   * A scene on the adult explore grid: a 16:9 still with a duration badge, the
   * site and date under it, and the performers under that.
   *
   * It is 16:9 rather than the 2:3 of DiscoverCard because a scene's artwork IS
   * a video still — a poster crop of one is a poster of nothing. It is also not
   * a link: there is no scene detail screen (a scene is not addressable on its
   * own; its number is its position in its site's release year), so the card's
   * only verb is the one on it.
   *
   * That verb is Request for EVERYBODY, admins included, and this is the same
   * rule the retired Scenes tab kept: approving a scene request adds the SITE,
   * so a one-click "add" here would be a button whose real effect is several
   * hundred scenes. Adding a site outright is the Sites shelf's Add button.
   */
  import type { SceneMeta } from '../api/types';
  import { durationBadge } from '../explore';
  import { scenePerformers } from '../adult';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import Poster from './Poster.svelte';

  interface Props {
    scene: SceneMeta;
    /** True while this card's Request call is in flight. */
    requesting?: boolean;
    /** True while ANY card's is — every button waits for it. */
    busy?: boolean;
    onrequest: (scene: SceneMeta) => void;
  }

  let { scene, requesting = false, busy = false, onrequest }: Props = $props();

  let duration = $derived(durationBadge(scene.duration));
  let performers = $derived(scenePerformers(scene));
  let meta = $derived([scene.site_name, scene.date].filter((part) => part !== '').join(' · '));

  const { t, tp } = useI18n();
</script>

<div class="flex w-full flex-col gap-2">
  <div class="relative overflow-hidden rounded-md border border-border bg-surface">
    <Poster path={scene.image_url} alt="" fallbackIcon="flame" aspect="video" />

    <!-- Owned beats requested, as everywhere else: once the library holds the
         scene the request is moot. -->
    {#if scene.in_library}
      <span class="absolute bottom-2 left-2">
        <Badge tone="success">
          <span class="inline-flex items-center gap-1"><Icon name="check" size={10} />{t('component.sceneCard.inLibrary')}</span>
        </Badge>
      </span>
    {:else if scene.requested}
      <span class="absolute bottom-2 left-2">
        <Badge tone="warning">
          <span class="inline-flex items-center gap-1"><Icon name="clock" size={10} />{t('component.sceneCard.requested')}</span>
        </Badge>
      </span>
    {:else}
      <span class="absolute bottom-2 left-2">
        <Button
          variant="primary"
          size="sm"
          disabled={busy}
          onclick={() => onrequest(scene)}>
          {requesting ? t('component.sceneCard.requesting') : t('component.sceneCard.request')}
        </Button>
      </span>
    {/if}

    {#if duration}
      <span class="absolute bottom-2 right-2">
        <Badge mono tone="neutral">{duration}</Badge>
      </span>
    {/if}
  </div>

  <div class="min-w-0">
    <p class="truncate text-base font-medium text-ink" title={scene.title}>{scene.title}</p>
    {#if meta}
      <p class="truncate text-sm text-ink-secondary" title={meta}>{meta}</p>
    {/if}
    {#if performers.length > 0}
      <p class="truncate text-sm text-ink-muted" title={performers.join(', ')}>
        {performers.join(', ')}
      </p>
    {/if}
  </div>
</div>
