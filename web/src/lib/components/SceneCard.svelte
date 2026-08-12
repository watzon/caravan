<script lang="ts">
  import { useI18n } from '../i18n.svelte';
  /**
   * A scene on the adult explore grid: a 16:9 still with a duration badge, the
   * site and date under it, and the performers under that.
   *
   * A provider scene is addressable by its provider and opaque stash id. The
   * artwork and title are real links to that durable detail URL; Request stays
   * separate so the card never nests a button inside an anchor.
   *
   * That verb is Request for EVERYBODY, admins included: approving a scene
   * request adds the SITE, so a one-click "add" here would add several hundred
   * scenes. Adding a site outright is the Sites shelf's Add button.
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
  let href = $derived(
    `/adult/scenes/${encodeURIComponent(scene.provider)}/${encodeURIComponent(scene.stash_id)}`,
  );

  let duration = $derived(durationBadge(scene.duration));
  let performers = $derived(scenePerformers(scene));
  let meta = $derived([scene.site_name, scene.date].filter((part) => part !== '').join(' · '));

  const { t, tp } = useI18n();
</script>

<div class="flex w-full flex-col gap-2">
  <div class="relative overflow-hidden rounded-md border border-border bg-surface">
    <a
      {href}
      aria-label={scene.title}
      class="group/card block rounded-md focus:outline-none focus-visible:ring-2 focus-visible:ring-accent">
      <Poster path={scene.image_url} alt="" fallbackIcon="flame" aspect="video" />
    </a>

    <!-- Owned beats requested, as everywhere else: once the library holds the
         scene the request is moot. -->
    {#if scene.in_library}
      <span class="pointer-events-none absolute bottom-2 left-2">
        <Badge tone="success">
          <span class="inline-flex items-center gap-1"><Icon name="check" size={10} />{t('component.sceneCard.inLibrary')}</span>
        </Badge>
      </span>
    {:else if scene.requested}
      <span class="pointer-events-none absolute bottom-2 left-2">
        <Badge tone="warning">
          <span class="inline-flex items-center gap-1"><Icon name="clock" size={10} />{t('component.sceneCard.requested')}</span>
        </Badge>
      </span>
    {:else}
      <span class="absolute bottom-2 left-2 z-10">
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
      <span class="pointer-events-none absolute bottom-2 right-2">
        <Badge mono tone="neutral">{duration}</Badge>
      </span>
    {/if}
  </div>

  <div class="min-w-0">
    <a
      {href}
      class="block truncate text-base font-medium text-ink hover:text-accent-text focus:outline-none focus-visible:rounded focus-visible:ring-2 focus-visible:ring-accent"
      title={scene.title}>
      {scene.title}
    </a>
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
