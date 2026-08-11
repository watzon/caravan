import {
  translate,
  type TranslationKey,
  type Translator,
} from '../i18n.svelte';

export const SETTINGS_CATEGORIES = [
  'libraryMedia',
  'searchDownloads',
  'playbackSharing',
  'automation',
  'system',
] as const;

export type SettingsCategory = (typeof SETTINGS_CATEGORIES)[number];

const CATEGORY_KEYS: Record<SettingsCategory, TranslationKey> = {
  libraryMedia: 'settings.category.libraryMedia',
  searchDownloads: 'settings.category.searchDownloads',
  playbackSharing: 'settings.category.playbackSharing',
  automation: 'settings.category.automation',
  system: 'settings.category.system',
};

/**
 * One destination for the settings overview, search, and in-page navigation.
 *
 * `route` always names a live settings route. `anchor` identifies a section
 * within that route when the destination has one; callers use `settingsHref`
 * rather than building fragments themselves.
 */
export interface SettingsCatalogEntry {
  category: SettingsCategory;
  labelKey: TranslationKey;
  descriptionKey: TranslationKey;
  searchKey: TranslationKey;
  route: string;
  anchor?: string;
  advanced?: boolean;
  narrow?: boolean;
}

export const SETTINGS_CATALOG = [
  {
    category: 'libraryMedia',
    labelKey: 'settings.entry.libraries.label',
    descriptionKey: 'settings.entry.libraries.description',
    searchKey: 'settings.entry.libraries.search',
    route: '/settings/libraries',
    anchor: 'libraries',
  },
  {
    category: 'libraryMedia',
    labelKey: 'settings.entry.metadata.label',
    descriptionKey: 'settings.entry.metadata.description',
    searchKey: 'settings.entry.metadata.search',
    route: '/settings/metadata',
    anchor: 'metadata',
  },
  {
    category: 'libraryMedia',
    labelKey: 'settings.entry.qualityProfiles.label',
    descriptionKey: 'settings.entry.qualityProfiles.description',
    searchKey: 'settings.entry.qualityProfiles.search',
    route: '/settings/quality-profiles',
    anchor: 'quality-profiles',
  },
  {
    category: 'libraryMedia',
    labelKey: 'settings.entry.storage.label',
    descriptionKey: 'settings.entry.storage.description',
    searchKey: 'settings.entry.storage.search',
    route: '/settings/storage',
    anchor: 'storage',
  },
  {
    category: 'searchDownloads',
    labelKey: 'settings.entry.indexers.label',
    descriptionKey: 'settings.entry.indexers.description',
    searchKey: 'settings.entry.indexers.search',
    route: '/settings/indexers',
    anchor: 'indexers',
  },
  {
    category: 'searchDownloads',
    labelKey: 'settings.entry.downloads.label',
    descriptionKey: 'settings.entry.downloads.description',
    searchKey: 'settings.entry.downloads.search',
    route: '/settings/downloads',
    anchor: 'downloads',
    advanced: true,
  },
  {
    category: 'playbackSharing',
    labelKey: 'settings.entry.playback.label',
    descriptionKey: 'settings.entry.playback.description',
    searchKey: 'settings.entry.playback.search',
    route: '/settings/playback',
    anchor: 'playback',
  },
  {
    category: 'automation',
    labelKey: 'settings.entry.notifications.label',
    descriptionKey: 'settings.entry.notifications.description',
    searchKey: 'settings.entry.notifications.search',
    route: '/settings/notifications',
    anchor: 'notifications',
  },
  {
    category: 'automation',
    labelKey: 'settings.entry.tasks.label',
    descriptionKey: 'settings.entry.tasks.description',
    searchKey: 'settings.entry.tasks.search',
    route: '/settings/tasks',
    anchor: 'tasks',
  },
  {
    category: 'system',
    labelKey: 'settings.entry.users.label',
    descriptionKey: 'settings.entry.users.description',
    searchKey: 'settings.entry.users.search',
    route: '/settings/users',
    anchor: 'users',
  },
  {
    category: 'system',
    labelKey: 'settings.entry.interface.label',
    descriptionKey: 'settings.entry.interface.description',
    searchKey: 'settings.entry.interface.search',
    route: '/settings/interface',
    anchor: 'interface',
    narrow: true,
  },
  {
    category: 'system',
    labelKey: 'settings.entry.security.label',
    descriptionKey: 'settings.entry.security.description',
    searchKey: 'settings.entry.security.search',
    route: '/settings/security',
    anchor: 'security',
  },
] as const satisfies readonly SettingsCatalogEntry[];

export function settingsCategoryLabel(
  category: SettingsCategory,
  t: Translator = translate,
): string {
  return t(CATEGORY_KEYS[category]);
}

export function settingsLabel(
  entry: SettingsCatalogEntry,
  t: Translator = translate,
): string {
  return t(entry.labelKey);
}

export function settingsDescription(
  entry: SettingsCatalogEntry,
  t: Translator = translate,
): string {
  return t(entry.descriptionKey);
}

export function settingsHref(entry: SettingsCatalogEntry): string {
  return entry.anchor ? `${entry.route}#${entry.anchor}` : entry.route;
}

/**
 * Retired route slugs stay addressable so bookmarks and links can keep working
 * while every settings navigation surface highlights the page they resolve to.
 */
const LEGACY_SETTINGS_SECTIONS: Readonly<Record<string, string>> = {
  engine: 'downloads',
  'usenet-servers': 'downloads',
  'download-clients': 'downloads',
  dlna: 'playback',
  jellyfin: 'playback',
  'tv-profile': 'playback',
  general: 'metadata',
  // The adult module's own page dissolved into the library cards: its master
  // switch is a library's Active toggle and its member list is a library's
  // Access card, so a bookmark lands on the screen that now holds both.
  adult: 'libraries',
};

/** The catalog entry a direct or retired `/settings/:section` route represents. */
export function settingsEntryForSection(section: string): SettingsCatalogEntry {
  const currentSection = LEGACY_SETTINGS_SECTIONS[section] ?? section;
  return (
    SETTINGS_CATALOG.find((entry) => entry.route === `/settings/${currentSection}`) ??
    SETTINGS_CATALOG.find((entry) => entry.route === '/settings/metadata')!
  );
}

export function settingsMatches(
  entry: SettingsCatalogEntry,
  query: string,
  t: Translator = translate,
): boolean {
  const term = query.trim().toLocaleLowerCase();
  if (!term) return true;

  return [settingsLabel(entry, t), settingsDescription(entry, t), t(entry.searchKey)]
    .join(' ')
    .toLocaleLowerCase()
    .includes(term);
}
