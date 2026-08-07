export const SETTINGS_CATEGORIES = [
  'Library and media',
  'Search and downloads',
  'Playback and sharing',
  'Automation',
  'System',
] as const;

export type SettingsCategory = (typeof SETTINGS_CATEGORIES)[number];

/**
 * One destination for the settings overview, search, and in-page navigation.
 *
 * `route` always names a live settings route. `anchor` identifies a section
 * within that route when the destination has one; callers use `settingsHref`
 * rather than building fragments themselves.
 */
export interface SettingsCatalogEntry {
  category: SettingsCategory;
  label: string;
  description: string;
  route: string;
  anchor?: string;
  aliases: readonly string[];
  keywords: readonly string[];
  advanced?: boolean;
  narrow?: boolean;
}

export const SETTINGS_CATALOG = [
  {
    category: 'Library and media',
    label: 'Libraries',
    description: 'Create and organise movie, series, and adult libraries.',
    route: '/settings/libraries',
    anchor: 'libraries',
    aliases: ['library', 'media folders'],
    // Who reaches a library is a library setting now, so the words somebody
    // would have searched for on the retired Adult content page have to land
    // here — including 'adult', which names a KIND of library rather than a
    // module.
    keywords: [
      'movies',
      'series',
      'root folder',
      'access',
      'privacy',
      'restricted',
      'active',
      'adult',
    ],
  },
  {
    category: 'Library and media',
    label: 'Metadata',
    description: 'Configure each metadata provider: TMDB, AniList, and more.',
    route: '/settings/metadata',
    anchor: 'metadata',
    aliases: ['TMDB', 'AniList', 'API key', 'providers'],
    keywords: ['metadata key', 'artwork', 'titles', 'discover', 'anime', 'thetvdb', 'tvdb', 'stash-box', 'stashdb', 'fansdb'],
  },
  {
    category: 'Library and media',
    label: 'Quality profiles',
    description: 'Choose release quality and upgrade rules for each library.',
    route: '/settings/quality-profiles',
    anchor: 'quality-profiles',
    aliases: ['profiles', 'profile', 'release profiles'],
    keywords: ['quality', 'upgrade', 'release', 'source preference', 'proper', 'repack', 'seeders', 'size limit', 'custom format', 'TV compatibility', 'import', 'export'],
  },
  {
    category: 'Library and media',
    label: 'Storage',
    description: 'Set the root that contains the library, downloads, and database.',
    route: '/settings/storage',
    anchor: 'storage',
    aliases: ['storage root', 'data directory', 'database'],
    keywords: ['disk', 'files', 'migration', 'naming', 'recycle', 'retention', 'backup', 'restore'],
  },
  {
    category: 'Search and downloads',
    label: 'Indexers',
    description: 'Add Torznab and Newznab search sources.',
    route: '/settings/indexers',
    anchor: 'indexers',
    aliases: ['Prowlarr', 'search providers'],
    keywords: ['torznab', 'newznab', 'RSS', 'priority'],
  },
  {
    category: 'Search and downloads',
    label: 'Downloads',
    description: 'Set download concurrency, engine ports, Usenet servers, and external clients.',
    route: '/settings/downloads',
    anchor: 'downloads',
    aliases: ['download client', 'torrent engine'],
    keywords: ['port', 'Usenet', 'NZB', 'seeding', 'concurrency'],
    advanced: true,
  },
  {
    category: 'Playback and sharing',
    label: 'Playback',
    description: 'Configure DLNA sharing, Jellyfin handoff, and TV compatibility.',
    route: '/settings/playback',
    anchor: 'playback',
    aliases: ['DLNA', 'Jellyfin', 'media server'],
    keywords: ['sharing', 'streaming', 'TV profile', 'transcoding'],
  },
  {
    category: 'Automation',
    label: 'Notifications',
    description: 'Send grabs, imports, and health problems to webhooks.',
    route: '/settings/notifications',
    anchor: 'notifications',
    aliases: ['connect', 'webhooks'],
    keywords: ['notification', 'grab', 'import', 'health', 'automation'],
  },
  {
    category: 'Automation',
    label: 'Tasks',
    description: 'Review scheduled background work and its next run.',
    route: '/settings/tasks',
    anchor: 'tasks',
    aliases: ['schedule', 'scheduled tasks'],
    keywords: ['automation', 'timer', 'job', 'interval', 'cadence'],
  },
  {
    category: 'System',
    label: 'Users',
    description: 'Manage who can sign in and what each person may do.',
    route: '/settings/users',
    anchor: 'users',
    aliases: ['accounts', 'members'],
    keywords: ['login', 'roles', 'access'],
  },
  {
    category: 'System',
    label: 'Interface',
    description: 'Choose this browser’s theme and motion preferences.',
    route: '/settings/interface',
    anchor: 'interface',
    aliases: ['appearance', 'display'],
    keywords: ['theme', 'dark', 'light', 'motion', 'accessibility'],
    narrow: true,
  },
  {
    category: 'System',
    label: 'Security',
    description: 'Change your password and manage the API key for external tools.',
    route: '/settings/security',
    anchor: 'security',
    aliases: ['API token', 'API key'],
    keywords: ['password', 'authentication', 'calendar'],
  },
] as const satisfies readonly SettingsCatalogEntry[];

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

export function settingsMatches(entry: SettingsCatalogEntry, query: string): boolean {
  const term = query.trim().toLocaleLowerCase();
  if (!term) return true;

  return [entry.label, entry.description, ...entry.aliases, ...entry.keywords]
    .join(' ')
    .toLocaleLowerCase()
    .includes(term);
}
