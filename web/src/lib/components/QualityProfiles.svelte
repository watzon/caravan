<script lang="ts">
  import { useI18n } from '../i18n.svelte';
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
    if (qualities.length === 2) {
      return t('component.qualityProfiles.twoQualities', {
        first: qualities[0]!,
        second: qualities[1]!,
      });
    }
    return t('component.qualityProfiles.qualityList', {
      leading: qualities.slice(0, -1).join(', '),
      last: qualities.at(-1)!,
    });
  }

  function policySummary(profile: Omit<QualityProfileDraft, 'name'>): string {
    const accepted = t('component.qualityProfiles.accepts', { qualities: joinedQualities(profile.items) });
    const upgrades = profile.upgradeAllowed
      ? t('component.qualityProfiles.upgrades', { cutoff: profile.cutoff })
      : t('component.qualityProfiles.keepsFirst', { cutoff: profile.cutoff });
    const sources = profile.preferredSources.length > 0
      ? t('component.qualityProfiles.sourceOrder', { sources: profile.preferredSources.join(', ') })
      : t('component.qualityProfiles.sourceBuiltIn');
    const proper = profile.properRepackPreference === 'prefer'
      ? t('component.qualityProfiles.properAddsScore')
      : t('component.qualityProfiles.properNeutral');
    const seeders = profile.minSeeders === '0'
      ? t('component.qualityProfiles.noSeederMinimum')
      : t('component.qualityProfiles.seederMinimum', { count: profile.minSeeders });
    const minimum = profile.minSizeMB === '0'
      ? t('component.qualityProfiles.noMinimum')
      : t('component.qualityProfiles.minimumSize', { count: profile.minSizeMB });
    const maximum = profile.maxSizeMB === '0'
      ? t('component.qualityProfiles.noMaximum')
      : t('component.qualityProfiles.maximumSize', { count: profile.maxSizeMB });
    const size = profile.minSizeMB === '0' && profile.maxSizeMB === '0'
      ? t('component.qualityProfiles.sizeUnrestricted')
      : t('component.qualityProfiles.knownSize', { minimum, maximum });
    const formats = tp('component.qualityProfiles.customFormatScore', profile.customFormats.length);
    const target = profile.tvProfile === 'safe'
      ? t('component.qualityProfiles.safeTv')
      : t('component.qualityProfiles.capableTv');
    const tv = t('component.qualityProfiles.tvTarget', {
      target,
      policy: profile.tvCompatibilityPolicy,
    });
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
    if (profile.is_default) reasons.push(t('component.qualityProfiles.systemDefaultReason'));
    if (profile.assignments.libraries > 0 || titles > 0) {
      reasons.push(t('component.qualityProfiles.assignedReason', { assignment: assignmentSummary(profile) }));
    }
    return reasons.join(' ');
  }

  function parseNonNegativeInteger(value: string, label: string): { value: number | null; error: string | null } {
    if (!/^\d+$/.test(value.trim())) return { value: null, error: t('component.qualityProfiles.wholeNumber', { label }) };
    return { value: Number(value), error: null };
  }

  function parseTerms(value: string): string[] {
    return value.split(',').map((term) => term.trim());
  }

  function validateDraft(): DraftValidation {
    const minSeedersResult = parseNonNegativeInteger(minSeeders, t('component.qualityProfiles.minimumSeeders'));
    const minSizeResult = parseNonNegativeInteger(minSizeMB, t('component.qualityProfiles.minimumSize'));
    const maxSizeResult = parseNonNegativeInteger(maxSizeMB, t('component.qualityProfiles.maximumSize'));
    const customFormatErrors: CustomFormatErrors[] = [];
    const parsedFormats = customFormats.map((format) => {
      const includeTerms = parseTerms(format.includeTerms);
      const excludeTerms = format.excludeTerms === '' ? [] : parseTerms(format.excludeTerms);
      const score = Number(format.score);
      const errors: CustomFormatErrors = {
        name: format.name.trim() === '' ? t('component.qualityProfiles.ruleNameRequired') : null,
        includeTerms: includeTerms.length === 0 || includeTerms.some((term) => term === '')
          ? t('component.qualityProfiles.includeTermsRequired')
          : null,
        excludeTerms: excludeTerms.some((term) => term === '')
          ? t('component.qualityProfiles.blankExcludeTerms')
          : null,
        score: format.score.trim() === '' || !Number.isInteger(score)
          ? t('component.qualityProfiles.scoreWholeNumber')
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
          ? t('component.qualityProfiles.maximumAtLeastMinimum')
          : null);
    const nameError = name.trim() === '' ? t('component.qualityProfiles.profileNameRequired') : null;
    const itemsError = items.length === 0 ? t('component.qualityProfiles.qualityRequired') : null;
    const cutoffError = !items.includes(cutoff) ? t('component.qualityProfiles.cutoffRequired') : null;
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
      pushToast(editing ? t('component.qualityProfiles.profileUpdated') : t('component.qualityProfiles.profileCreated'), 'success');
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        serverNameError = t('component.qualityProfiles.duplicateProfile');
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
        throw new Error(t('component.qualityProfiles.jsonObjectRequired'));
      }
      importFilename = file.name;
      importPayload = parsed as QualityProfileImport;
      importConfirmOpen = true;
    } catch (err) {
      importError = err instanceof Error ? t('component.qualityProfiles.cannotRead', { name: file.name, message: err.message }) : t('component.qualityProfiles.cannotReadFile', { name: file.name });
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
      pushToast(tp('component.qualityProfiles.importedProfiles', result.profiles), 'success');
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
      pushToast(t('component.qualityProfiles.systemDefaultName', { name: updated.name }), 'success');
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
      testError = t('component.qualityProfiles.releaseNameRequired');
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
      pushToast(t('component.qualityProfiles.profileDeleted'), 'success');
    } catch (err) {
      deleteError = errorText(err);
    } finally {
      deletingBusy = false;
    }
  }

  const { t, tp } = useI18n();
</script>

<div class="flex max-w-5xl flex-col gap-5">
  <div class="flex flex-wrap items-center gap-3">
    <p class="flex-1 text-base text-ink-secondary">{t('component.qualityProfiles.defineTheReleasesCaravanAcceptsWhenItShouldKeepSearchingAndWhereEachPolicyIsInUse')}</p>
    <div class="flex flex-wrap items-center gap-2">
      <Button variant="secondary" href={api.exportQualityProfilesURL()}>{t('component.qualityProfiles.exportJson')}</Button>
      <input
        bind:this={importInput}
        class="sr-only"
        type="file"
        accept="application/json,.json"
        aria-label={t('component.qualityProfiles.chooseQualityProfileJsonFile')}
        onchange={selectImport} />
      <Button variant="secondary" onclick={() => importInput?.click()}>{t('component.qualityProfiles.importJson')}</Button>
      <Button variant="primary" onclick={openCreate}>{t('component.qualityProfiles.newProfile')}</Button>
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
    <EmptyState icon="settings" title={t('component.qualityProfiles.noQualityProfiles')} message={t('component.qualityProfiles.createAProfileToDefineWhichReleasesCaravanCanAccept')} />
  {:else}
    <ul class="overflow-hidden rounded-md border border-border bg-surface">
      {#each profiles ?? [] as profile (profile.id)}
        {@const deleteReason = deletionReason(profile)}
        <li class="flex flex-wrap items-start gap-x-5 gap-y-3 border-b border-border px-4 py-4 last:border-b-0">
          <div class="min-w-56 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <p class="font-medium text-ink">{profile.name}</p>
              {#if profile.is_default}<Badge tone="accent">{t('component.qualityProfiles.systemDefault')}</Badge>{/if}
            </div>
            <p class="mt-1 text-sm leading-5 text-ink-secondary">{policySummary(profilePolicyDraft(profile))}</p>
            <p class="mt-2 text-sm text-ink-secondary">
              {t('component.qualityProfiles.directAssignments', { assignment: assignmentSummary(profile) })}
              <span class="text-ink-muted">{t('component.qualityProfiles.assignmentCounts', { movies: profile.assignments.movies, series: profile.assignments.series })}</span>
            </p>
            {#if deleteReason}
              <p class="mt-1 text-sm text-ink-secondary">{t('component.qualityProfiles.deletionUnavailable', { reason: deleteReason })}</p>
            {:else}
              <p class="mt-1 text-sm text-ink-secondary">{t('component.qualityProfiles.deletionAvailable')}</p>
            {/if}
          </div>

          <div class="flex flex-wrap items-center gap-1" aria-label={t('component.qualityProfiles.acceptedResolutions', { name: profile.name })}>
            {#each QUALITY_LADDER.filter((item) => profile.items.includes(item)) as item (item)}
              <Badge mono tone="neutral">{item}</Badge>
            {/each}
          </div>
          <Badge tone="warning" mono title={profile.upgrade_allowed ? t('component.qualityProfiles.upgradeCutoff') : t('component.qualityProfiles.cutoffWithoutUpgrades')}>
            {profile.upgrade_allowed ? t('component.qualityProfiles.upgradeTo', { cutoff: profile.cutoff }) : t('component.qualityProfiles.cutoffValue', { cutoff: profile.cutoff })}
          </Badge>

          <div class="ml-auto flex flex-wrap items-center justify-end gap-2">
            {#if !profile.is_default}
              <Button variant="secondary" size="sm" disabled={defaultingID !== null} onclick={() => setDefault(profile)}>
                {defaultingID === profile.id ? t('component.qualityProfiles.settingDefault') : t('component.qualityProfiles.setDefault')}}
              </Button>
            {/if}
            <Button variant="secondary" size="sm" onclick={() => openClone(profile)}>{t('component.qualityProfiles.clone')}</Button>
            <Button variant="secondary" size="sm" onclick={() => openEdit(profile)}>{t('component.qualityProfiles.edit')}</Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={deleteReason !== ''}
              title={deleteReason || undefined}
              onclick={() => { deleting = profile; deleteError = null; }}>{t('component.qualityProfiles.delete')}</Button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</div>

{#if formOpen}
  <Modal title={editing ? t('component.qualityProfiles.editProfile') : t('component.qualityProfiles.newQualityProfile')} width="max-w-4xl" dirty={dirty} onclose={closeForm}>
    <form class="flex flex-col gap-6 p-4" onsubmit={(event) => { event.preventDefault(); save(); }}>
      <section class="flex flex-col gap-5" aria-labelledby="quality-profile-core-heading">
        <h3 id="quality-profile-core-heading" class="font-medium text-ink">{t('component.qualityProfiles.coreQualityRules')}</h3>
        <Field label={t('component.qualityProfiles.name')} for="quality-profile-name" error={nameError}>
          <TextInput id="quality-profile-name" bind:value={name} oninput={() => (serverNameError = null)} autofocus />
        </Field>

        <Field label={t('component.qualityProfiles.allowedQualities')}>
          <div
            class="grid grid-cols-2 gap-2"
            role="group"
            aria-label={t('component.qualityProfiles.allowedQualities')}
            aria-describedby={validation.items ? 'quality-profile-items-error' : 'quality-profile-items-help'}>
            {#each QUALITY_LADDER as item (item)}
              <label class="flex items-center gap-2 rounded-sm border border-border bg-raised px-3 py-2 text-base text-ink">
                <input type="checkbox" checked={items.includes(item)} onchange={() => toggleItem(item)} class="size-4 accent-accent" />
                <span class="font-mono text-sm">{item}</span>
              </label>
            {/each}
          </div>
          {#if items.length > 0}
            <div class="flex flex-wrap gap-1" aria-label={t('component.qualityProfiles.selectedQualityOrder')}>
              {#each items as item (item)}<Badge mono tone="neutral">{item}</Badge>{/each}
            </div>
          {/if}
          {#if validation.items}
            <p id="quality-profile-items-error" class="text-sm text-danger" role="alert">{validation.items}</p>
          {:else}
            <p id="quality-profile-items-help" class="text-sm text-ink-secondary">{t('component.qualityProfiles.qualitiesStayInTheFixedBestFirstOrder')}</p>
          {/if}
        </Field>

        <Field
          label={t('component.qualityProfiles.cutoff')}
          for="quality-profile-cutoff">
          <select
            id="quality-profile-cutoff"
            bind:value={cutoff}
            aria-invalid={validation.cutoff ? 'true' : undefined}
            aria-describedby={validation.cutoff ? 'quality-profile-cutoff-error' : 'quality-profile-cutoff-help'}
            class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink focus:border-accent focus:outline-none">
            {#each items as item (item)}<option value={item}>{item}</option>{/each}
          </select>
          {#if validation.cutoff}
            <p id="quality-profile-cutoff-error" class="text-sm text-danger" role="alert">{validation.cutoff}</p>
          {:else}
            <p id="quality-profile-cutoff-help" class="text-sm text-ink-secondary">{t('component.qualityProfiles.allowedQualitiesPlusThisCutoffDefineWhichReleasesAreAccepted')}</p>
          {/if}
        </Field>

        <Toggle checked={upgradeAllowed} label={t('component.qualityProfiles.allowUpgradesAboveTheCutoff')} onchange={(next) => (upgradeAllowed = next)} />
      </section>

      <section class="flex flex-col gap-5 border-t border-border pt-5" aria-labelledby="quality-profile-source-heading">
        <div class="flex flex-wrap items-center gap-2">
          <h3 id="quality-profile-source-heading" class="font-medium text-ink">{t('component.qualityProfiles.sourceAndProtocol')}</h3>
          <Badge tone="neutral">{t('component.qualityProfiles.advanced')}</Badge>
        </div>
        <Field label={t('component.qualityProfiles.sourceOrder')}>
          <ol class="flex flex-col gap-2" aria-label={t('component.qualityProfiles.preferredSourceOrder')} aria-describedby="quality-profile-source-order-help">
            {#each sourceOrder as source, index (source)}
              <li class="flex flex-wrap items-center gap-3 rounded-sm border border-border bg-raised px-3 py-2">
                <span class="w-8 shrink-0 font-mono text-sm text-ink-muted">{index + 1}</span>
                <span class="min-w-0 flex-1 font-mono text-sm text-ink">{source}</span>
                <div class="flex w-full flex-wrap justify-end gap-1 sm:w-auto">
                  <Button variant="secondary" size="sm" disabled={index === 0} onclick={() => moveSource(index, -1)}>Move {source} up</Button>
                  <Button variant="secondary" size="sm" disabled={index === sourceOrder.length - 1} onclick={() => moveSource(index, 1)}>Move {source} down</Button>
                </div>
              </li>
            {/each}
          </ol>
          <div><Button variant="ghost" size="sm" disabled={preferredSources.length === 0} onclick={resetSourceOrder}>{t('component.qualityProfiles.resetToBuiltInOrder')}</Button></div>
          <p id="quality-profile-source-order-help" class="text-sm text-ink-secondary">{t('component.qualityProfiles.preferredSourcesAffectRankingMoveSourcesToSetTheirPreferredOrder')}</p>
        </Field>

        <Field label={t('component.qualityProfiles.properAndRepackPreference')}>
          <div
            class="grid gap-2 sm:grid-cols-2"
            role="radiogroup"
            aria-label={t('component.qualityProfiles.properAndRepackPreference')}
            aria-describedby="quality-profile-proper-repack-help">
            <label class="flex items-start gap-2 rounded-sm border border-border bg-raised px-3 py-3 text-sm text-ink">
              <input type="radio" name="proper-repack-preference" value="prefer" checked={properRepackPreference === 'prefer'} onchange={() => (properRepackPreference = 'prefer')} class="mt-0.5 accent-accent" />
              <span><span class="block font-medium">{t('component.qualityProfiles.prefer')}</span><span class="text-ink-secondary">{t('component.qualityProfiles.addsScoreForProperAndRepackTags')}</span></span>
            </label>
            <label class="flex items-start gap-2 rounded-sm border border-border bg-raised px-3 py-3 text-sm text-ink">
              <input type="radio" name="proper-repack-preference" value="neutral" checked={properRepackPreference === 'neutral'} onchange={() => (properRepackPreference = 'neutral')} class="mt-0.5 accent-accent" />
              <span><span class="block font-medium">{t('component.qualityProfiles.neutral')}</span><span class="text-ink-secondary">{t('component.qualityProfiles.ignoresProperAndRepackTags')}</span></span>
            </label>
          </div>
          <p id="quality-profile-proper-repack-help" class="text-sm text-ink-secondary">{t('component.qualityProfiles.preferAddsScoreNeutralIgnoresProperAndRepackTags')}</p>
        </Field>

        <Field label={t('component.qualityProfiles.minimumTorrentSeeders')} for="quality-profile-min-seeders">
          <input id="quality-profile-min-seeders" type="number" min="0" step="1" value={minSeeders} oninput={(event) => (minSeeders = (event.currentTarget as HTMLInputElement).value)} aria-invalid={validation.minSeeders ? 'true' : undefined} aria-describedby={validation.minSeeders ? 'quality-profile-min-seeders-error' : 'quality-profile-min-seeders-help'} class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink focus:border-accent focus:outline-none" />
          {#if validation.minSeeders}
            <p id="quality-profile-min-seeders-error" class="text-sm text-danger" role="alert">{validation.minSeeders}</p>
          {:else}
            <p id="quality-profile-min-seeders-help" class="text-sm text-ink-secondary">{t('component.qualityProfiles.appliesToTorrentReleasesOnlyZeroDisablesTheMinimum')}</p>
          {/if}
        </Field>
      </section>

      <section class="flex flex-col gap-5 border-t border-border pt-5" aria-labelledby="quality-profile-size-heading">
        <div class="flex flex-wrap items-center gap-2">
          <h3 id="quality-profile-size-heading" class="font-medium text-ink">{t('component.qualityProfiles.sizeAndAvailability')}</h3>
          <Badge tone="neutral">{t('component.qualityProfiles.advanced')}</Badge>
        </div>
        <p class="text-sm leading-5 text-ink-secondary">{t('component.qualityProfiles.sizeLimitsApplyOnlyWhenCaravanKnowsTheReleaseSizeZeroDisablesThatBound')}</p>
        <div class="grid gap-5 sm:grid-cols-2">
          <Field label={t('component.qualityProfiles.minimumSizeMb')} for="quality-profile-min-size">
            <input id="quality-profile-min-size" type="number" min="0" step="1" value={minSizeMB} oninput={(event) => (minSizeMB = (event.currentTarget as HTMLInputElement).value)} aria-invalid={validation.minSizeMB ? 'true' : undefined} aria-describedby={validation.minSizeMB ? 'quality-profile-min-size-error' : undefined} class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink focus:border-accent focus:outline-none" />
            {#if validation.minSizeMB}<p id="quality-profile-min-size-error" class="text-sm text-danger" role="alert">{validation.minSizeMB}</p>{/if}
          </Field>
          <Field label={t('component.qualityProfiles.maximumSizeMb')} for="quality-profile-max-size">
            <input id="quality-profile-max-size" type="number" min="0" step="1" value={maxSizeMB} oninput={(event) => (maxSizeMB = (event.currentTarget as HTMLInputElement).value)} aria-invalid={validation.maxSizeMB ? 'true' : undefined} aria-describedby={validation.maxSizeMB ? 'quality-profile-max-size-error' : undefined} class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink focus:border-accent focus:outline-none" />
            {#if validation.maxSizeMB}<p id="quality-profile-max-size-error" class="text-sm text-danger" role="alert">{validation.maxSizeMB}</p>{/if}
          </Field>
        </div>
      </section>

      <section class="flex flex-col gap-5 border-t border-border pt-5" aria-labelledby="quality-profile-tv-heading">
        <div class="flex flex-wrap items-center gap-2">
          <h3 id="quality-profile-tv-heading" class="font-medium text-ink">{t('component.qualityProfiles.tvPlayback')}</h3>
          <Badge tone="neutral">{t('component.qualityProfiles.advanced')}</Badge>
        </div>
        <Field label={t('component.qualityProfiles.tvProfile')}>
          <div class="grid gap-2 sm:grid-cols-2" role="radiogroup" aria-label={t('component.qualityProfiles.tvProfile')}>
            <label class="flex items-start gap-2 rounded-sm border border-border bg-raised px-3 py-3 text-sm text-ink">
              <input type="radio" name="tv-profile" value="safe" checked={tvProfile === 'safe'} onchange={() => (tvProfile = 'safe')} class="mt-0.5 accent-accent" />
              <span><span class="block font-medium">{t('component.qualityProfiles.safe')}</span><span class="text-ink-secondary">{t('component.qualityProfiles.h264AacMp4Target')}</span></span>
            </label>
            <label class="flex items-start gap-2 rounded-sm border border-border bg-raised px-3 py-3 text-sm text-ink">
              <input type="radio" name="tv-profile" value="capable" checked={tvProfile === 'capable'} onchange={() => (tvProfile = 'capable')} class="mt-0.5 accent-accent" />
              <span><span class="block font-medium">{t('component.qualityProfiles.capable')}</span><span class="text-ink-secondary">{t('component.qualityProfiles.hevcAacMkvTarget')}</span></span>
            </label>
          </div>
        </Field>
        <Field
          label={t('component.qualityProfiles.compatibilityPolicy')}>
          <div
            class="grid gap-2 sm:grid-cols-3"
            role="radiogroup"
            aria-label={t('component.qualityProfiles.tvCompatibilityPolicy')}
            aria-describedby="quality-profile-compatibility-help">
            {#each TV_COMPATIBILITY_POLICIES as policy (policy)}
              <label class="flex items-center gap-2 rounded-sm border border-border bg-raised px-3 py-2 text-sm text-ink">
                <input type="radio" name="tv-compatibility-policy" value={policy} checked={tvCompatibilityPolicy === policy} onchange={() => (tvCompatibilityPolicy = policy)} class="accent-accent" />
                <span class="capitalize">{policy}</span>
              </label>
            {/each}
          </div>
          <p id="quality-profile-compatibility-help" class="text-sm text-ink-secondary">{t('component.qualityProfiles.preferAddsACompatibilityBonusRequireRejectsReleasesThatDoNotMatchTheSelectedTvTarget')}</p>
        </Field>
      </section>

      <section class="flex flex-col gap-4 border-t border-border pt-5" aria-labelledby="quality-profile-formats-heading">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <div class="flex flex-wrap items-center gap-2">
              <h3 id="quality-profile-formats-heading" class="font-medium text-ink">{t('component.qualityProfiles.customFormats')}</h3>
              <Badge tone="neutral">{t('component.qualityProfiles.advanced')}</Badge>
            </div>
            <p class="mt-1 text-sm leading-5 text-ink-secondary">{t('component.qualityProfiles.customFormatScoresAddToTheReleaseTotalNegativeScoresPenalizeMatchingReleases')}</p>
          </div>
          <Button variant="secondary" size="sm" onclick={addCustomFormat}>{t('component.qualityProfiles.addCustomFormat')}</Button>
        </div>
        {#each customFormats as format, index (index)}
          {@const formatErrors = validation.customFormats[index]!}
          <div class="flex flex-col gap-4 rounded-sm border border-border bg-raised p-3">
            <div class="flex items-center justify-between gap-3">
              <p class="font-medium text-ink">Rule {index + 1}</p>
              <Button variant="ghost" size="sm" onclick={() => removeCustomFormat(index)}>{t('component.qualityProfiles.remove')}</Button>
            </div>
            <div class="grid gap-4 sm:grid-cols-2">
              <Field label={t('component.qualityProfiles.ruleName')} for={`quality-format-name-${index}`} error={formatErrors.name}>
                <TextInput id={`quality-format-name-${index}`} value={format.name} oninput={(event) => updateCustomFormat(index, { name: (event.currentTarget as HTMLInputElement).value })} />
              </Field>
              <Field label={t('component.qualityProfiles.score')} for={`quality-format-score-${index}`}>
                <input id={`quality-format-score-${index}`} type="number" step="1" value={format.score} oninput={(event) => updateCustomFormat(index, { score: (event.currentTarget as HTMLInputElement).value })} aria-invalid={formatErrors.score ? 'true' : undefined} aria-describedby={formatErrors.score ? `quality-format-score-${index}-error` : `quality-format-score-${index}-help`} class="h-9 w-full rounded-sm border border-border-strong bg-surface px-3 text-md text-ink focus:border-accent focus:outline-none" />
                {#if formatErrors.score}
                  <p id={`quality-format-score-${index}-error`} class="text-sm text-danger" role="alert">{formatErrors.score}</p>
                {:else}
                  <p id={`quality-format-score-${index}-help`} class="text-sm text-ink-secondary">{t('component.qualityProfiles.negativeScoresPenalizeMatchingReleases')}</p>
                {/if}
              </Field>
            </div>
            <Field label={t('component.qualityProfiles.includeTerms')} for={`quality-format-include-${index}`} error={formatErrors.includeTerms} help={t('component.qualityProfiles.separateTermsWithCommasAtLeastOneTermIsRequired')}>
              <TextInput id={`quality-format-include-${index}`} value={format.includeTerms} oninput={(event) => updateCustomFormat(index, { includeTerms: (event.currentTarget as HTMLInputElement).value })} />
            </Field>
            <Field label={t('component.qualityProfiles.excludeTerms')} for={`quality-format-exclude-${index}`} error={formatErrors.excludeTerms} help={t('component.qualityProfiles.separateTermsWithCommasLeaveBlankWhenNoneApply')}>
              <TextInput id={`quality-format-exclude-${index}`} value={format.excludeTerms} oninput={(event) => updateCustomFormat(index, { excludeTerms: (event.currentTarget as HTMLInputElement).value })} />
            </Field>
          </div>
        {/each}
      </section>

      <p class="rounded-sm border border-border bg-raised px-3 py-2 text-sm leading-5 text-ink-secondary" data-profile-summary>{draftSummary}</p>

      <section class="flex flex-col gap-3 border-t border-border pt-5" aria-labelledby="quality-profile-test-heading">
        <div>
          <h3 id="quality-profile-test-heading" class="font-medium text-ink">{t('component.qualityProfiles.testReleaseNames')}</h3>
          <p class="mt-1 text-sm text-ink-secondary">{t('component.qualityProfiles.pasteOneReleaseNamePerLineCaravanParsesAndScoresEachNameOnTheServerThenShowsEachScoreContributionBelow')}</p>
        </div>
        {#if editing}
          <Field label={t('component.qualityProfiles.releaseNames')} for="quality-profile-test-titles">
            <textarea
              id="quality-profile-test-titles"
              bind:value={testTitles}
              aria-invalid={testError ? 'true' : undefined}
              aria-describedby={testError ? 'quality-profile-test-titles-error' : 'quality-profile-test-titles-help'}
              rows="5"
              disabled={testing || dirty}
              class="w-full resize-y rounded-sm border border-border-strong bg-raised px-3 py-2 font-mono text-sm text-ink focus:border-accent focus:outline-none disabled:opacity-50"
              placeholder={t('component.qualityProfiles.example20261080PWebDl')}
            ></textarea>
            {#if testError}
              <p id="quality-profile-test-titles-error" class="text-sm text-danger" role="alert">{testError}</p>
            {:else}
              <p id="quality-profile-test-titles-help" class="text-sm text-ink-secondary">
                {dirty ? t('component.qualityProfiles.dirtyTestHelp') : t('component.qualityProfiles.oneReleaseNamePerLine')}}
              </p>
            {/if}
          </Field>
          <div>
            <Button variant="secondary" disabled={testing || dirty} onclick={runTest}>{testing ? t('component.qualityProfiles.testing') : t('component.qualityProfiles.testReleases')}</Button>
          </div>

          {#if testResults !== null}
            <ol class="flex flex-col gap-2" aria-live="polite">
              {#each testResults as result, index (index)}
                <li class="border border-border bg-raised px-3 py-3">
                  <div class="flex flex-wrap items-start justify-between gap-2">
                    <p class="min-w-0 flex-1 break-words font-mono text-sm text-ink">{result.title}</p>
                    <div class="flex shrink-0 items-center gap-1">
                      <Badge tone={result.decision.accepted ? 'success' : 'danger'}>{result.decision.accepted ? t('component.qualityProfiles.accepted') : t('component.qualityProfiles.rejected')}</Badge>
                      <Badge mono tone="neutral">Score {result.decision.score}</Badge>
                    </div>
                  </div>
                  <div class="mt-2 flex flex-wrap gap-1 text-sm">
                    <Badge mono tone="neutral">{result.parsed.quality || t('component.qualityProfiles.unknownResolution')}</Badge>
                    <Badge mono tone="neutral">{result.parsed.source || t('component.qualityProfiles.unknownSource')}</Badge>
                  </div>
                  <p class="mt-2 text-sm leading-5 text-ink-secondary">{result.decision.reason}</p>
                  <dl class="mt-3 grid grid-cols-2 gap-x-4 gap-y-1 text-sm sm:grid-cols-5">
                    <div><dt class="text-ink-muted">{t('component.qualityProfiles.quality')}</dt><dd class="font-medium text-ink">{result.decision.contributions.quality}</dd></div>
                    <div><dt class="text-ink-muted">{t('component.qualityProfiles.source')}</dt><dd class="font-medium text-ink">{result.decision.contributions.source}</dd></div>
                    <div><dt class="text-ink-muted">{t('component.qualityProfiles.proper')}</dt><dd class="font-medium text-ink">{result.decision.contributions.proper}</dd></div>
                    <div><dt class="text-ink-muted">{t('component.qualityProfiles.repack')}</dt><dd class="font-medium text-ink">{result.decision.contributions.repack}</dd></div>
                    <div><dt class="text-ink-muted">{t('component.qualityProfiles.seeders')}</dt><dd class="font-medium text-ink">{result.decision.contributions.seeders}</dd></div>
                  </dl>
                </li>
              {/each}
            </ol>
          {/if}
        {:else}
          <p class="text-sm text-ink-secondary">{t('component.qualityProfiles.saveThisProfileBeforeTestingReleaseNames')}</p>
        {/if}
      </section>
    </form>
    {#snippet footer()}
      <div class="flex w-full flex-wrap justify-end gap-2">
        <Button variant="ghost" onclick={closeForm}>{t('component.qualityProfiles.cancel')}</Button>
        <Button variant="primary" disabled={saving || !dirty || !formValid} onclick={save}>
          {saving ? t('component.qualityProfiles.saving') : !dirty ? t('component.qualityProfiles.noChanges') : editing ? t('component.qualityProfiles.saveChanges') : t('component.qualityProfiles.createProfile')}
        </Button>
      </div>
    {/snippet}
  </Modal>
{/if}

{#if importConfirmOpen}
  <Modal title={t('component.qualityProfiles.importQualityProfiles')} width="max-w-md" onclose={() => { importConfirmOpen = false; importError = null; }}>
    <div class="flex flex-col gap-3 p-4">
      <p class="text-base text-ink-secondary">{t('component.qualityProfiles.import')} <span class="font-medium text-ink">{importFilename}</span>?</p>
      <p class="text-sm leading-5 text-ink-secondary">{t('component.qualityProfiles.thisUpdatesSameNameProfilesCreatesMissingProfilesKeepsUnlistedProfilesAndChangesTheSystemDefault')}</p>
      {#if importError}<p class="text-sm text-danger" role="alert">{importError}</p>{/if}
    </div>
    {#snippet footer()}
      <div class="flex w-full flex-wrap justify-end gap-2">
        <Button variant="ghost" disabled={importing} onclick={() => { importConfirmOpen = false; importError = null; }}>{t('component.qualityProfiles.cancel')}</Button>
        <Button variant="danger" disabled={importing} onclick={confirmImport}>{importing ? t('component.qualityProfiles.importing') : t('component.qualityProfiles.importProfiles')}</Button>
      </div>
    {/snippet}
  </Modal>
{/if}

{#if deleting}
  {@const profile = deleting}
  <Modal title={t('component.qualityProfiles.deleteQualityProfile')} width="max-w-md" onclose={() => (deleting = null)}>
    <div class="flex flex-col gap-3 p-4">
      <p class="text-base text-ink-secondary">{t('component.qualityProfiles.delete')} <span class="font-medium text-ink">{profile.name}</span>? This cannot be undone.</p>
      {#if deleteError}<p class="text-sm text-danger" role="alert">{deleteError}</p>{/if}
    </div>
    {#snippet footer()}
      <div class="flex w-full flex-wrap justify-end gap-2">
        <Button variant="ghost" disabled={deletingBusy} onclick={() => (deleting = null)}>{t('component.qualityProfiles.cancel')}</Button>
        <Button variant="danger" disabled={deletingBusy} onclick={confirmDelete}>{deletingBusy ? t('component.qualityProfiles.deleting') : t('component.qualityProfiles.delete')}</Button>
      </div>
    {/snippet}
  </Modal>
{/if}
