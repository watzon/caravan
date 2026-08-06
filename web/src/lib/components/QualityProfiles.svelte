<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError, api, errorText } from '../api/client';
  import type { ProfileTestResult, Quality, QualityProfile, QualityProfileInput } from '../api/types';
  import { QUALITY_LADDER } from '../api/types';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import EmptyState from './EmptyState.svelte';
  import Field from './Field.svelte';
  import LoadError from './LoadError.svelte';
  import Modal from './Modal.svelte';
  import Skeleton from './Skeleton.svelte';
  import TextInput from './TextInput.svelte';
  import Toggle from './Toggle.svelte';
  import { pushToast } from '../state/toast.svelte';

  const BUILT_IN_SOURCES = ['bluray', 'webdl', 'webrip', 'hdtv', 'dvd', 'cam'];
  const TV_COMPATIBILITY_POLICIES = ['ignore', 'prefer', 'require'] as const;

  type CustomFormatDraft = {
    name: string;
    includeTerms: string;
    excludeTerms: string;
    score: string;
  };

  type QualityProfileDraft = {
    name: string;
    items: Quality[];
    cutoff: Quality;
    upgradeAllowed: boolean;
    preferredSources: string[];
    properRepackPreference: 'prefer' | 'neutral';
    minSeeders: string;
    minSizeMB: string;
    maxSizeMB: string;
    customFormats: CustomFormatDraft[];
    tvProfile: 'safe' | 'capable';
    tvCompatibilityPolicy: 'ignore' | 'prefer' | 'require';
  };

  type CustomFormatErrors = {
    name: string | null;
    includeTerms: string | null;
    excludeTerms: string | null;
    score: string | null;
  };

  type DraftValidation = {
    body: QualityProfileInput | null;
    name: string | null;
    items: string | null;
    cutoff: string | null;
    minSeeders: string | null;
    minSizeMB: string | null;
    maxSizeMB: string | null;
    customFormats: CustomFormatErrors[];
  };

  type QualityProfileImport = Parameters<typeof api.importQualityProfiles>[0];

  let profiles = $state<QualityProfile[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let formOpen = $state(false);
  let editing = $state<QualityProfile | null>(null);
  let deleting = $state<QualityProfile | null>(null);
  let deletingBusy = $state(false);
  let deleteError = $state<string | null>(null);
  let defaultingID = $state<number | null>(null);
  let saving = $state(false);

  let name = $state('');
  let items = $state<Quality[]>([]);
  let cutoff = $state<Quality>('1080p');
  let upgradeAllowed = $state(true);
  let preferredSources = $state<string[]>([]);
  let properRepackPreference = $state<'prefer' | 'neutral'>('prefer');
  let minSeeders = $state('0');
  let minSizeMB = $state('0');
  let maxSizeMB = $state('0');
  let customFormats = $state<CustomFormatDraft[]>([]);
  let tvProfile = $state<'safe' | 'capable'>('safe');
  let tvCompatibilityPolicy = $state<'ignore' | 'prefer' | 'require'>('ignore');
  let serverNameError = $state<string | null>(null);
  let initialDraft = $state<QualityProfileDraft | null>(null);
  let testTitles = $state('');
  let testing = $state(false);
  let testResults = $state<ProfileTestResult[] | null>(null);
  let testError = $state<string | null>(null);
  let importInput = $state<HTMLInputElement | null>(null);
  let importFilename = $state<string | null>(null);
  let importPayload = $state<QualityProfileImport | null>(null);
  let importError = $state<string | null>(null);
  let importConfirmOpen = $state(false);
  let importing = $state(false);


  function customFormatsFromProfile(profile: QualityProfile | null): CustomFormatDraft[] {
    return (profile?.custom_formats ?? []).map((format) => ({
      name: format.name,
      includeTerms: format.include_terms.join(', '),
      excludeTerms: format.exclude_terms.join(', '),
      score: String(format.score),
    }));
  }

  function draft(): QualityProfileDraft {
    return {
      name,
      items: [...items],
      cutoff,
      upgradeAllowed,
      preferredSources: [...preferredSources],
      properRepackPreference,
      minSeeders,
      minSizeMB,
      maxSizeMB,
      customFormats: customFormats.map((format) => ({ ...format })),
      tvProfile,
      tvCompatibilityPolicy,
    };
  }

  function sameDraft(left: QualityProfileDraft, right: QualityProfileDraft): boolean {
    return left.name === right.name
      && left.cutoff === right.cutoff
      && left.upgradeAllowed === right.upgradeAllowed
      && left.properRepackPreference === right.properRepackPreference
      && left.minSeeders === right.minSeeders
      && left.minSizeMB === right.minSizeMB
      && left.maxSizeMB === right.maxSizeMB
      && left.tvProfile === right.tvProfile
      && left.tvCompatibilityPolicy === right.tvCompatibilityPolicy
      && left.items.length === right.items.length
      && left.items.every((item, index) => item === right.items[index])
      && left.preferredSources.length === right.preferredSources.length
      && left.preferredSources.every((source, index) => source === right.preferredSources[index])
      && left.customFormats.length === right.customFormats.length
      && left.customFormats.every((format, index) => {
        const other = right.customFormats[index];
        return other !== undefined
          && format.name === other.name
          && format.includeTerms === other.includeTerms
          && format.excludeTerms === other.excludeTerms
          && format.score === other.score;
      });
  }

  function joinedQualities(qualities: Quality[]): string {
    if (qualities.length === 1) return qualities[0]!;
    if (qualities.length === 2) return `${qualities[0]} and ${qualities[1]}`;
    return `${qualities.slice(0, -1).join(', ')}, and ${qualities.at(-1)}`;
  }

  function policySummary(profile: Omit<QualityProfileDraft, 'name'>): string {
    const accepted = `Accepts ${joinedQualities(profile.items)}.`;
    const upgrades = profile.upgradeAllowed
      ? `Upgrades toward the ${profile.cutoff} cutoff.`
      : `Keeps the first accepted release; ${profile.cutoff} is the cutoff.`;
    const sources = profile.preferredSources.length > 0
      ? `Source order: ${profile.preferredSources.join(', ')}.`
      : 'Source order: built-in.';
    const proper = profile.properRepackPreference === 'prefer'
      ? 'Proper and repack tags add score.'
      : 'Proper and repack tags are neutral.';
    const seeders = profile.minSeeders === '0'
      ? 'No torrent seeder minimum.'
      : `At least ${profile.minSeeders} torrent seeders.`;
    const size = profile.minSizeMB === '0' && profile.maxSizeMB === '0'
      ? 'Release size is unrestricted.'
      : `Known release size: ${profile.minSizeMB === '0' ? 'no minimum' : `${profile.minSizeMB} MB minimum`}; ${profile.maxSizeMB === '0' ? 'no maximum' : `${profile.maxSizeMB} MB maximum`}.`;
    const formats = profile.customFormats.length === 0
      ? 'No custom format scores.'
      : `${profile.customFormats.length} custom format ${profile.customFormats.length === 1 ? 'score' : 'scores'}.`;
    const tv = `${profile.tvProfile === 'safe' ? 'Safe H.264/AAC/MP4' : 'Capable HEVC/AAC/MKV'} TV target; compatibility ${profile.tvCompatibilityPolicy}.`;
    return [accepted, upgrades, sources, proper, seeders, size, formats, tv].join(' ');
  }

  function profilePolicyDraft(profile: QualityProfile): Omit<QualityProfileDraft, 'name'> {
    return {
      items: QUALITY_LADDER.filter((item) => profile.items.includes(item)),
      cutoff: profile.cutoff,
      upgradeAllowed: profile.upgrade_allowed,
      preferredSources: profile.preferred_sources ?? [],
      properRepackPreference: profile.proper_repack_preference ?? 'prefer',
      minSeeders: String(profile.min_seeders ?? 0),
      minSizeMB: String(profile.min_size_mb ?? 0),
      maxSizeMB: String(profile.max_size_mb ?? 0),
      customFormats: customFormatsFromProfile(profile),
      tvProfile: profile.tv_profile ?? 'safe',
      tvCompatibilityPolicy: profile.tv_compatibility_policy ?? 'ignore',
    };
  }

  function assignmentSummary(profile: QualityProfile): string {
    const titles = profile.assignments.movies + profile.assignments.series;
    return `${profile.assignments.libraries} ${profile.assignments.libraries === 1 ? 'library' : 'libraries'} · ${titles} ${titles === 1 ? 'title' : 'titles'}`;
  }

  function deletionReason(profile: QualityProfile): string {
    const reasons: string[] = [];
    const titles = profile.assignments.movies + profile.assignments.series;
    if (profile.is_default) reasons.push('It is the system default.');
    if (profile.assignments.libraries > 0 || titles > 0) {
      reasons.push(`It is assigned to ${assignmentSummary(profile)}.`);
    }
    return reasons.join(' ');
  }

  function parseNonNegativeInteger(value: string, label: string): { value: number | null; error: string | null } {
    if (!/^\d+$/.test(value.trim())) return { value: null, error: `${label} must be a whole number of zero or more.` };
    return { value: Number(value), error: null };
  }

  function parseTerms(value: string): string[] {
    return value.split(',').map((term) => term.trim());
  }

  function validateDraft(): DraftValidation {
    const minSeedersResult = parseNonNegativeInteger(minSeeders, 'Minimum seeders');
    const minSizeResult = parseNonNegativeInteger(minSizeMB, 'Minimum size');
    const maxSizeResult = parseNonNegativeInteger(maxSizeMB, 'Maximum size');
    const customFormatErrors: CustomFormatErrors[] = [];
    const parsedFormats = customFormats.map((format) => {
      const includeTerms = parseTerms(format.includeTerms);
      const excludeTerms = format.excludeTerms === '' ? [] : parseTerms(format.excludeTerms);
      const score = Number(format.score);
      const errors: CustomFormatErrors = {
        name: format.name.trim() === '' ? 'Enter a rule name.' : null,
        includeTerms: includeTerms.length === 0 || includeTerms.some((term) => term === '')
          ? 'Enter one or more include terms, separated by commas.'
          : null,
        excludeTerms: excludeTerms.some((term) => term === '')
          ? 'Remove blank exclude terms.'
          : null,
        score: format.score.trim() === '' || !Number.isInteger(score)
          ? 'Score must be a whole number.'
          : null,
      };
      customFormatErrors.push(errors);
      return {
        name: format.name.trim(),
        include_terms: includeTerms,
        exclude_terms: excludeTerms,
        score,
      };
    });
    const maxSizeError = maxSizeResult.error
      ?? (minSizeResult.value !== null
        && maxSizeResult.value !== null
        && minSizeResult.value !== 0
        && maxSizeResult.value !== 0
        && maxSizeResult.value < minSizeResult.value
          ? 'Maximum size must be at least the minimum size when both limits are enabled.'
          : null);
    const nameError = name.trim() === '' ? 'Enter a profile name.' : null;
    const itemsError = items.length === 0 ? 'Select at least one quality.' : null;
    const cutoffError = !items.includes(cutoff) ? 'Choose a cutoff from the selected qualities.' : null;
    const hasCustomFormatError = customFormatErrors.some((errors) =>
      Object.values(errors).some(Boolean),
    );
    const hasError = Boolean(
      nameError
      || itemsError
      || cutoffError
      || minSeedersResult.error
      || minSizeResult.error
      || maxSizeError
      || hasCustomFormatError,
    );
    return {
      body: hasError ? null : {
        name: name.trim(),
        items: [...items],
        cutoff,
        upgrade_allowed: upgradeAllowed,
        preferred_sources: [...preferredSources],
        proper_repack_preference: properRepackPreference,
        min_seeders: minSeedersResult.value!,
        min_size_mb: minSizeResult.value!,
        max_size_mb: maxSizeResult.value!,
        custom_formats: parsedFormats,
        tv_profile: tvProfile,
        tv_compatibility_policy: tvCompatibilityPolicy,
      },
      name: nameError,
      items: itemsError,
      cutoff: cutoffError,
      minSeeders: minSeedersResult.error,
      minSizeMB: minSizeResult.error,
      maxSizeMB: maxSizeError,
      customFormats: customFormatErrors,
    };
  }

  let validation = $derived(validateDraft());
  let dirty = $derived(initialDraft !== null && !sameDraft(draft(), initialDraft));
  let formValid = $derived(validation.body !== null);
  let nameError = $derived(serverNameError ?? validation.name);
  let sourceOrder = $derived(preferredSources.length > 0 ? [...preferredSources] : [...BUILT_IN_SOURCES]);
  let draftSummary = $derived(policySummary({
    items,
    cutoff,
    upgradeAllowed,
    preferredSources,
    properRepackPreference,
    minSeeders,
    minSizeMB,
    maxSizeMB,
    customFormats,
    tvProfile,
    tvCompatibilityPolicy,
  }));

  async function load() {
    loading = true;
    try {
      profiles = await api.listQualityProfiles();
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  function resetTestPanel() {
    testTitles = '';
    testResults = null;
    testError = null;
  }

  function resetForm(profile: QualityProfile | null) {
    editing = profile;
    name = profile?.name ?? '';
    items = profile ? QUALITY_LADDER.filter((item) => profile.items.includes(item)) : ['1080p'];
    cutoff = profile?.cutoff ?? '1080p';
    upgradeAllowed = profile?.upgrade_allowed ?? true;
    preferredSources = profile?.preferred_sources ?? [];
    properRepackPreference = profile?.proper_repack_preference ?? 'prefer';
    minSeeders = String(profile?.min_seeders ?? 0);
    minSizeMB = String(profile?.min_size_mb ?? 0);
    maxSizeMB = String(profile?.max_size_mb ?? 0);
    customFormats = customFormatsFromProfile(profile);
    tvProfile = profile?.tv_profile ?? 'safe';
    tvCompatibilityPolicy = profile?.tv_compatibility_policy ?? 'ignore';
    serverNameError = null;
    resetTestPanel();
    initialDraft = draft();
  }

  function openCreate() {
    resetForm(null);
    formOpen = true;
  }

  function openEdit(profile: QualityProfile) {
    resetForm(profile);
    formOpen = true;
  }

  function suggestedCloneName(sourceName: string): string {
    const names = new Set((profiles ?? []).map((profile) => profile.name.trim().toLocaleLowerCase()));
    const base = `${sourceName} copy`;
    if (!names.has(base.toLocaleLowerCase())) return base;

    let ordinal = 2;
    while (names.has(`${base} ${ordinal}`.toLocaleLowerCase())) ordinal += 1;
    return `${base} ${ordinal}`;
  }

  function openClone(profile: QualityProfile) {
    resetForm(profile);
    editing = null;
    name = suggestedCloneName(profile.name);
    initialDraft = { ...draft(), name: '' };
    formOpen = true;
  }

  function closeForm() {
    formOpen = false;
    initialDraft = null;
  }

  function toggleItem(item: Quality) {
    if (items.includes(item)) {
      items = items.filter((candidate) => candidate !== item);
      if (cutoff === item) cutoff = items[0] ?? '1080p';
      return;
    }
    const next = QUALITY_LADDER.filter((candidate) => candidate === item || items.includes(candidate));
    items = next;
    if (!next.includes(cutoff)) cutoff = next[0]!;
  }

  function moveSource(index: number, direction: -1 | 1) {
    const nextIndex = index + direction;
    if (nextIndex < 0 || nextIndex >= sourceOrder.length) return;
    const next = [...sourceOrder];
    [next[index], next[nextIndex]] = [next[nextIndex]!, next[index]!];
    preferredSources = next;
  }

  function resetSourceOrder() {
    preferredSources = [];
  }

  function addCustomFormat() {
    customFormats = [...customFormats, { name: '', includeTerms: '', excludeTerms: '', score: '0' }];
  }

  function updateCustomFormat(index: number, changes: Partial<CustomFormatDraft>) {
    customFormats = customFormats.map((format, candidate) =>
      candidate === index ? { ...format, ...changes } : format,
    );
  }

  function removeCustomFormat(index: number) {
    customFormats = customFormats.filter((_, candidate) => candidate !== index);
  }

  async function save() {
    if (saving || !dirty) return;
    const body = validateDraft().body;
    if (!body) return;
    saving = true;
    try {
      const profile = editing
        ? await api.updateQualityProfile(editing.id, body)
        : await api.addQualityProfile(body);
      profiles = editing
        ? (profiles ?? []).map((current) => current.id === profile.id ? profile : current)
        : [...(profiles ?? []), profile];
      closeForm();
      pushToast(editing ? 'Quality profile updated.' : 'Quality profile created.', 'success');
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        serverNameError = 'A profile with that name already exists.';
      } else {
        pushToast(errorText(err), 'danger');
      }
    } finally {
      saving = false;
    }
  }

  async function selectImport(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = '';
    if (!file) return;
    importFilename = null;
    importPayload = null;
    importError = null;
    try {
      const parsed: unknown = JSON.parse(await file.text());
      if (parsed === null || typeof parsed !== 'object') {
        throw new Error('Choose a JSON object exported by Caravan.');
      }
      importFilename = file.name;
      importPayload = parsed as QualityProfileImport;
      importConfirmOpen = true;
    } catch (err) {
      importError = err instanceof Error ? `Cannot read ${file.name}: ${err.message}` : `Cannot read ${file.name}.`;
    }
  }

  async function confirmImport() {
    if (!importPayload || importing) return;
    importing = true;
    importError = null;
    try {
      const result = await api.importQualityProfiles(importPayload);
      await load();
      importConfirmOpen = false;
      importPayload = null;
      importFilename = null;
      pushToast(`Imported ${result.profiles} quality ${result.profiles === 1 ? 'profile' : 'profiles'}.`, 'success');
    } catch (err) {
      importError = errorText(err);
    } finally {
      importing = false;
    }
  }

  async function setDefault(profile: QualityProfile) {
    if (profile.is_default || defaultingID !== null) return;
    defaultingID = profile.id;
    try {
      const updated = await api.setDefaultQualityProfile(profile.id);
      profiles = (profiles ?? []).map((current) =>
        current.id === updated.id ? updated : { ...current, is_default: false },
      );
      pushToast(`${updated.name} is now the system default.`, 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      defaultingID = null;
    }
  }

  async function runTest() {
    if (!editing || testing || dirty) return;
    const titles = testTitles.split('\n').map((title) => title.trim()).filter(Boolean);
    if (titles.length === 0) {
      testError = 'Enter at least one release name.';
      testResults = null;
      return;
    }

    testing = true;
    testError = null;
    testResults = null;
    try {
      const response = await api.testQualityProfile(editing.id, { titles });
      testResults = response.results;
    } catch (err) {
      testError = errorText(err);
    } finally {
      testing = false;
    }
  }

  async function confirmDelete() {
    const profile = deleting;
    if (!profile) return;
    deletingBusy = true;
    deleteError = null;
    try {
      await api.deleteQualityProfile(profile.id);
      profiles = (profiles ?? []).filter((current) => current.id !== profile.id);
      deleting = null;
      pushToast('Quality profile deleted.', 'success');
    } catch (err) {
      deleteError = errorText(err);
    } finally {
      deletingBusy = false;
    }
  }
</script>

<div class="flex max-w-5xl flex-col gap-5">
  <div class="flex flex-wrap items-center gap-3">
    <p class="flex-1 text-base text-ink-secondary">Define the releases Caravan accepts, when it should keep searching, and where each policy is in use.</p>
    <div class="flex flex-wrap items-center gap-2">
      <Button variant="secondary" href={api.exportQualityProfilesURL()}>Export JSON</Button>
      <input
        bind:this={importInput}
        class="sr-only"
        type="file"
        accept="application/json,.json"
        aria-label="Choose quality profile JSON file"
        onchange={selectImport} />
      <Button variant="secondary" onclick={() => importInput?.click()}>Import JSON</Button>
      <Button variant="primary" onclick={openCreate}>New profile</Button>
    </div>
  </div>
  {#if importError && !importConfirmOpen}
    <p class="text-sm text-danger" role="alert">{importError}</p>
  {/if}

  {#if error && profiles === null}
    <LoadError message={error} onretry={load} />
  {:else if loading && profiles === null}
    <div class="flex flex-col gap-2">{#each Array.from({ length: 3 }) as _, i (i)}<Skeleton class="h-28 w-full rounded-md" />{/each}</div>
  {:else if (profiles ?? []).length === 0}
    <EmptyState icon="settings" title="No quality profiles" message="Create a profile to define which releases Caravan can accept." />
  {:else}
    <ul class="overflow-hidden rounded-md border border-border bg-surface">
      {#each profiles ?? [] as profile (profile.id)}
        {@const deleteReason = deletionReason(profile)}
        <li class="flex flex-wrap items-start gap-x-5 gap-y-3 border-b border-border px-4 py-4 last:border-b-0">
          <div class="min-w-56 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <p class="font-medium text-ink">{profile.name}</p>
              {#if profile.is_default}<Badge tone="accent">System default</Badge>{/if}
            </div>
            <p class="mt-1 text-sm leading-5 text-ink-secondary">{policySummary(profilePolicyDraft(profile))}</p>
            <p class="mt-2 text-sm text-ink-secondary">
              Direct assignments: <span class="font-medium text-ink">{assignmentSummary(profile)}</span>
              <span class="text-ink-muted"> ({profile.assignments.movies} movies, {profile.assignments.series} series)</span>
            </p>
            {#if deleteReason}
              <p class="mt-1 text-sm text-ink-secondary">Deletion unavailable: {deleteReason}</p>
            {:else}
              <p class="mt-1 text-sm text-ink-secondary">Deletion available.</p>
            {/if}
          </div>

          <div class="flex flex-wrap items-center gap-1" aria-label={`${profile.name} accepted resolutions`}>
            {#each QUALITY_LADDER.filter((item) => profile.items.includes(item)) as item (item)}
              <Badge mono tone="neutral">{item}</Badge>
            {/each}
          </div>
          <Badge tone="warning" mono title={profile.upgrade_allowed ? 'Upgrade cutoff' : 'Cutoff without upgrades'}>
            {profile.upgrade_allowed ? `Upgrade to ${profile.cutoff}` : `Cutoff ${profile.cutoff}`}
          </Badge>

          <div class="ml-auto flex flex-wrap items-center justify-end gap-2">
            {#if !profile.is_default}
              <Button variant="secondary" size="sm" disabled={defaultingID !== null} onclick={() => setDefault(profile)}>
                {defaultingID === profile.id ? 'Setting default...' : 'Set as default'}
              </Button>
            {/if}
            <Button variant="secondary" size="sm" onclick={() => openClone(profile)}>Clone</Button>
            <Button variant="secondary" size="sm" onclick={() => openEdit(profile)}>Edit</Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={deleteReason !== ''}
              title={deleteReason || undefined}
              onclick={() => { deleting = profile; deleteError = null; }}>Delete</Button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</div>

{#if formOpen}
  <Modal title={editing ? 'Edit quality profile' : 'New quality profile'} width="max-w-4xl" dirty={dirty} onclose={closeForm}>
    <form class="flex flex-col gap-6 p-4" onsubmit={(event) => { event.preventDefault(); save(); }}>
      <section class="flex flex-col gap-5" aria-labelledby="quality-profile-core-heading">
        <h3 id="quality-profile-core-heading" class="font-medium text-ink">Core quality rules</h3>
        <Field label="Name" for="quality-profile-name" error={nameError}>
          <TextInput id="quality-profile-name" bind:value={name} oninput={() => (serverNameError = null)} autofocus />
        </Field>

        <Field label="Allowed qualities" error={validation.items} help="Qualities stay in the fixed best-first order.">
          <div class="grid grid-cols-2 gap-2" role="group" aria-label="Allowed qualities">
            {#each QUALITY_LADDER as item (item)}
              <label class="flex items-center gap-2 rounded-sm border border-border bg-raised px-3 py-2 text-base text-ink">
                <input type="checkbox" checked={items.includes(item)} onchange={() => toggleItem(item)} class="size-4 accent-accent" />
                <span class="font-mono text-sm">{item}</span>
              </label>
            {/each}
          </div>
          {#if items.length > 0}
            <div class="flex flex-wrap gap-1" aria-label="Selected quality order">
              {#each items as item (item)}<Badge mono tone="neutral">{item}</Badge>{/each}
            </div>
          {/if}
        </Field>

        <Field
          label="Cutoff"
          for="quality-profile-cutoff"
          error={validation.cutoff}
          help="Allowed qualities plus this cutoff define which releases are accepted.">
          <select id="quality-profile-cutoff" bind:value={cutoff} class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink focus:border-accent focus:outline-none">
            {#each items as item (item)}<option value={item}>{item}</option>{/each}
          </select>
        </Field>

        <Toggle checked={upgradeAllowed} label="Allow upgrades above the cutoff" onchange={(next) => (upgradeAllowed = next)} />
      </section>

      <section class="flex flex-col gap-5 border-t border-border pt-5" aria-labelledby="quality-profile-source-heading">
        <div class="flex flex-wrap items-center gap-2">
          <h3 id="quality-profile-source-heading" class="font-medium text-ink">Source and protocol</h3>
          <Badge tone="neutral">Advanced</Badge>
        </div>
        <Field label="Source order" help="Preferred sources affect ranking. Move sources to set their preferred order.">
          <ol class="flex flex-col gap-2" aria-label="Preferred source order">
            {#each sourceOrder as source, index (source)}
              <li class="flex items-center gap-3 rounded-sm border border-border bg-raised px-3 py-2">
                <span class="w-8 shrink-0 font-mono text-sm text-ink-muted">{index + 1}</span>
                <span class="min-w-0 flex-1 font-mono text-sm text-ink">{source}</span>
                <div class="flex shrink-0 gap-1">
                  <Button variant="secondary" size="sm" disabled={index === 0} onclick={() => moveSource(index, -1)}>Move {source} up</Button>
                  <Button variant="secondary" size="sm" disabled={index === sourceOrder.length - 1} onclick={() => moveSource(index, 1)}>Move {source} down</Button>
                </div>
              </li>
            {/each}
          </ol>
          <div><Button variant="ghost" size="sm" disabled={preferredSources.length === 0} onclick={resetSourceOrder}>Reset to built-in order</Button></div>
        </Field>

        <Field label="Proper and Repack preference" help="Prefer adds score. Neutral ignores proper and repack tags.">
          <div class="grid gap-2 sm:grid-cols-2" role="radiogroup" aria-label="Proper and Repack preference">
            <label class="flex items-start gap-2 rounded-sm border border-border bg-raised px-3 py-3 text-sm text-ink">
              <input type="radio" name="proper-repack-preference" value="prefer" checked={properRepackPreference === 'prefer'} onchange={() => (properRepackPreference = 'prefer')} class="mt-0.5 accent-accent" />
              <span><span class="block font-medium">Prefer</span><span class="text-ink-secondary">Adds score for proper and repack tags.</span></span>
            </label>
            <label class="flex items-start gap-2 rounded-sm border border-border bg-raised px-3 py-3 text-sm text-ink">
              <input type="radio" name="proper-repack-preference" value="neutral" checked={properRepackPreference === 'neutral'} onchange={() => (properRepackPreference = 'neutral')} class="mt-0.5 accent-accent" />
              <span><span class="block font-medium">Neutral</span><span class="text-ink-secondary">Ignores proper and repack tags.</span></span>
            </label>
          </div>
        </Field>

        <Field label="Minimum torrent seeders" for="quality-profile-min-seeders" error={validation.minSeeders} help="Applies to torrent releases only. Zero disables the minimum.">
          <input id="quality-profile-min-seeders" type="number" min="0" step="1" value={minSeeders} oninput={(event) => (minSeeders = (event.currentTarget as HTMLInputElement).value)} aria-invalid={validation.minSeeders ? 'true' : undefined} class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink focus:border-accent focus:outline-none" />
        </Field>
      </section>

      <section class="flex flex-col gap-5 border-t border-border pt-5" aria-labelledby="quality-profile-size-heading">
        <div class="flex flex-wrap items-center gap-2">
          <h3 id="quality-profile-size-heading" class="font-medium text-ink">Size and availability</h3>
          <Badge tone="neutral">Advanced</Badge>
        </div>
        <p class="text-sm leading-5 text-ink-secondary">Size limits apply only when Caravan knows the release size. Zero disables that bound.</p>
        <div class="grid gap-5 sm:grid-cols-2">
          <Field label="Minimum size (MB)" for="quality-profile-min-size" error={validation.minSizeMB}>
            <input id="quality-profile-min-size" type="number" min="0" step="1" value={minSizeMB} oninput={(event) => (minSizeMB = (event.currentTarget as HTMLInputElement).value)} aria-invalid={validation.minSizeMB ? 'true' : undefined} class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink focus:border-accent focus:outline-none" />
          </Field>
          <Field label="Maximum size (MB)" for="quality-profile-max-size" error={validation.maxSizeMB}>
            <input id="quality-profile-max-size" type="number" min="0" step="1" value={maxSizeMB} oninput={(event) => (maxSizeMB = (event.currentTarget as HTMLInputElement).value)} aria-invalid={validation.maxSizeMB ? 'true' : undefined} class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink focus:border-accent focus:outline-none" />
          </Field>
        </div>
      </section>

      <section class="flex flex-col gap-5 border-t border-border pt-5" aria-labelledby="quality-profile-tv-heading">
        <div class="flex flex-wrap items-center gap-2">
          <h3 id="quality-profile-tv-heading" class="font-medium text-ink">TV playback</h3>
          <Badge tone="neutral">Advanced</Badge>
        </div>
        <Field label="TV profile">
          <div class="grid gap-2 sm:grid-cols-2" role="radiogroup" aria-label="TV profile">
            <label class="flex items-start gap-2 rounded-sm border border-border bg-raised px-3 py-3 text-sm text-ink">
              <input type="radio" name="tv-profile" value="safe" checked={tvProfile === 'safe'} onchange={() => (tvProfile = 'safe')} class="mt-0.5 accent-accent" />
              <span><span class="block font-medium">Safe</span><span class="text-ink-secondary">H.264/AAC/MP4 target.</span></span>
            </label>
            <label class="flex items-start gap-2 rounded-sm border border-border bg-raised px-3 py-3 text-sm text-ink">
              <input type="radio" name="tv-profile" value="capable" checked={tvProfile === 'capable'} onchange={() => (tvProfile = 'capable')} class="mt-0.5 accent-accent" />
              <span><span class="block font-medium">Capable</span><span class="text-ink-secondary">HEVC/AAC/MKV target.</span></span>
            </label>
          </div>
        </Field>
        <Field
          label="Compatibility policy"
          help="Prefer adds a compatibility bonus. Require rejects releases that do not match the selected TV target.">
          <div class="grid gap-2 sm:grid-cols-3" role="radiogroup" aria-label="TV compatibility policy">
            {#each TV_COMPATIBILITY_POLICIES as policy (policy)}
              <label class="flex items-center gap-2 rounded-sm border border-border bg-raised px-3 py-2 text-sm text-ink">
                <input type="radio" name="tv-compatibility-policy" value={policy} checked={tvCompatibilityPolicy === policy} onchange={() => (tvCompatibilityPolicy = policy)} class="accent-accent" />
                <span class="capitalize">{policy}</span>
              </label>
            {/each}
          </div>
        </Field>
      </section>

      <section class="flex flex-col gap-4 border-t border-border pt-5" aria-labelledby="quality-profile-formats-heading">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <div class="flex flex-wrap items-center gap-2">
              <h3 id="quality-profile-formats-heading" class="font-medium text-ink">Custom formats</h3>
              <Badge tone="neutral">Advanced</Badge>
            </div>
            <p class="mt-1 text-sm leading-5 text-ink-secondary">Custom format scores add to the release total. Negative scores penalize matching releases.</p>
          </div>
          <Button variant="secondary" size="sm" onclick={addCustomFormat}>Add custom format</Button>
        </div>
        {#each customFormats as format, index (index)}
          {@const formatErrors = validation.customFormats[index]!}
          <div class="flex flex-col gap-4 rounded-sm border border-border bg-raised p-3">
            <div class="flex items-center justify-between gap-3">
              <p class="font-medium text-ink">Rule {index + 1}</p>
              <Button variant="ghost" size="sm" onclick={() => removeCustomFormat(index)}>Remove</Button>
            </div>
            <div class="grid gap-4 sm:grid-cols-2">
              <Field label="Rule name" for={`quality-format-name-${index}`} error={formatErrors.name}>
                <TextInput id={`quality-format-name-${index}`} value={format.name} oninput={(event) => updateCustomFormat(index, { name: (event.currentTarget as HTMLInputElement).value })} />
              </Field>
              <Field label="Score" for={`quality-format-score-${index}`} error={formatErrors.score} help="Negative scores penalize matching releases.">
                <input id={`quality-format-score-${index}`} type="number" step="1" value={format.score} oninput={(event) => updateCustomFormat(index, { score: (event.currentTarget as HTMLInputElement).value })} aria-invalid={formatErrors.score ? 'true' : undefined} class="h-9 w-full rounded-sm border border-border-strong bg-surface px-3 text-md text-ink focus:border-accent focus:outline-none" />
              </Field>
            </div>
            <Field label="Include terms" for={`quality-format-include-${index}`} error={formatErrors.includeTerms} help="Separate terms with commas. At least one term is required.">
              <TextInput id={`quality-format-include-${index}`} value={format.includeTerms} oninput={(event) => updateCustomFormat(index, { includeTerms: (event.currentTarget as HTMLInputElement).value })} />
            </Field>
            <Field label="Exclude terms" for={`quality-format-exclude-${index}`} error={formatErrors.excludeTerms} help="Separate terms with commas. Leave blank when none apply.">
              <TextInput id={`quality-format-exclude-${index}`} value={format.excludeTerms} oninput={(event) => updateCustomFormat(index, { excludeTerms: (event.currentTarget as HTMLInputElement).value })} />
            </Field>
          </div>
        {/each}
      </section>

      <p class="rounded-sm border border-border bg-raised px-3 py-2 text-sm leading-5 text-ink-secondary" data-profile-summary>{draftSummary}</p>

      <section class="flex flex-col gap-3 border-t border-border pt-5" aria-labelledby="quality-profile-test-heading">
        <div>
          <h3 id="quality-profile-test-heading" class="font-medium text-ink">Test release names</h3>
          <p class="mt-1 text-sm text-ink-secondary">Paste one release name per line. Caravan parses and scores each name on the server, then shows each score contribution below.</p>
        </div>
        {#if editing}
          <Field label="Release names" for="quality-profile-test-titles" error={testError} help={dirty ? 'Save policy changes before testing so the results match the saved profile.' : 'One release name per line.'}>
            <textarea
              id="quality-profile-test-titles"
              bind:value={testTitles}
              aria-invalid={testError ? 'true' : undefined}
              rows="5"
              disabled={testing || dirty}
              class="w-full resize-y rounded-sm border border-border-strong bg-raised px-3 py-2 font-mono text-sm text-ink focus:border-accent focus:outline-none disabled:opacity-50"
              placeholder="Example.2026.1080p.WEB-DL"
            ></textarea>
          </Field>
          <div>
            <Button variant="secondary" disabled={testing || dirty} onclick={runTest}>{testing ? 'Testing...' : 'Test releases'}</Button>
          </div>

          {#if testResults !== null}
            <ol class="flex flex-col gap-2" aria-live="polite">
              {#each testResults as result, index (index)}
                <li class="border border-border bg-raised px-3 py-3">
                  <div class="flex flex-wrap items-start justify-between gap-2">
                    <p class="min-w-0 flex-1 break-words font-mono text-sm text-ink">{result.title}</p>
                    <div class="flex shrink-0 items-center gap-1">
                      <Badge tone={result.decision.accepted ? 'success' : 'danger'}>{result.decision.accepted ? 'Accepted' : 'Rejected'}</Badge>
                      <Badge mono tone="neutral">Score {result.decision.score}</Badge>
                    </div>
                  </div>
                  <div class="mt-2 flex flex-wrap gap-1 text-sm">
                    <Badge mono tone="neutral">{result.parsed.quality || 'Unknown resolution'}</Badge>
                    <Badge mono tone="neutral">{result.parsed.source || 'Unknown source'}</Badge>
                  </div>
                  <p class="mt-2 text-sm leading-5 text-ink-secondary">{result.decision.reason}</p>
                  <dl class="mt-3 grid grid-cols-2 gap-x-4 gap-y-1 text-sm sm:grid-cols-5">
                    <div><dt class="text-ink-muted">Quality</dt><dd class="font-medium text-ink">{result.decision.contributions.quality}</dd></div>
                    <div><dt class="text-ink-muted">Source</dt><dd class="font-medium text-ink">{result.decision.contributions.source}</dd></div>
                    <div><dt class="text-ink-muted">Proper</dt><dd class="font-medium text-ink">{result.decision.contributions.proper}</dd></div>
                    <div><dt class="text-ink-muted">Repack</dt><dd class="font-medium text-ink">{result.decision.contributions.repack}</dd></div>
                    <div><dt class="text-ink-muted">Seeders</dt><dd class="font-medium text-ink">{result.decision.contributions.seeders}</dd></div>
                  </dl>
                </li>
              {/each}
            </ol>
          {/if}
        {:else}
          <p class="text-sm text-ink-secondary">Save this profile before testing release names.</p>
        {/if}
      </section>
    </form>
    {#snippet footer()}
      <div class="flex w-full flex-wrap justify-end gap-2">
        <Button variant="ghost" onclick={closeForm}>Cancel</Button>
        <Button variant="primary" disabled={saving || !dirty || !formValid} onclick={save}>
          {saving ? 'Saving...' : !dirty ? 'No changes' : editing ? 'Save changes' : 'Create profile'}
        </Button>
      </div>
    {/snippet}
  </Modal>
{/if}

{#if importConfirmOpen}
  <Modal title="Import quality profiles" width="max-w-md" onclose={() => { importConfirmOpen = false; importError = null; }}>
    <div class="flex flex-col gap-3 p-4">
      <p class="text-base text-ink-secondary">Import <span class="font-medium text-ink">{importFilename}</span>?</p>
      <p class="text-sm leading-5 text-ink-secondary">This updates same-name profiles, creates missing profiles, keeps unlisted profiles, and changes the system default.</p>
      {#if importError}<p class="text-sm text-danger" role="alert">{importError}</p>{/if}
    </div>
    {#snippet footer()}
      <div class="flex w-full flex-wrap justify-end gap-2">
        <Button variant="ghost" disabled={importing} onclick={() => { importConfirmOpen = false; importError = null; }}>Cancel</Button>
        <Button variant="danger" disabled={importing} onclick={confirmImport}>{importing ? 'Importing...' : 'Import profiles'}</Button>
      </div>
    {/snippet}
  </Modal>
{/if}

{#if deleting}
  {@const profile = deleting}
  <Modal title="Delete quality profile" width="max-w-md" onclose={() => (deleting = null)}>
    <div class="flex flex-col gap-3 p-4">
      <p class="text-base text-ink-secondary">Delete <span class="font-medium text-ink">{profile.name}</span>? This cannot be undone.</p>
      {#if deleteError}<p class="text-sm text-danger">{deleteError}</p>{/if}
    </div>
    {#snippet footer()}
      <div class="flex w-full flex-wrap justify-end gap-2">
        <Button variant="ghost" disabled={deletingBusy} onclick={() => (deleting = null)}>Cancel</Button>
        <Button variant="danger" disabled={deletingBusy} onclick={confirmDelete}>{deletingBusy ? 'Deleting...' : 'Delete'}</Button>
      </div>
    {/snippet}
  </Modal>
{/if}
