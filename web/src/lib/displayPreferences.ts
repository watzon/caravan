export type ThemePreference = 'system' | 'dark' | 'light';
export type MotionPreference = 'system' | 'reduced';

export interface DisplayPreferences {
  theme: ThemePreference;
  motion: MotionPreference;
}

const STORAGE_KEY = 'caravan.display-preferences';
const DEFAULTS: DisplayPreferences = { theme: 'system', motion: 'system' };

function isTheme(value: unknown): value is ThemePreference {
  return value === 'system' || value === 'dark' || value === 'light';
}

function isMotion(value: unknown): value is MotionPreference {
  return value === 'system' || value === 'reduced';
}

export function readDisplayPreferences(): DisplayPreferences {
  try {
    const parsed = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? 'null') as Partial<DisplayPreferences> | null;
    return {
      theme: isTheme(parsed?.theme) ? parsed.theme : DEFAULTS.theme,
      motion: isMotion(parsed?.motion) ? parsed.motion : DEFAULTS.motion,
    };
  } catch {
    return { ...DEFAULTS };
  }
}

/** The theme the document is showing after System is resolved. */
export function resolvedTheme(theme: ThemePreference): 'dark' | 'light' {
  if (theme !== 'system') return theme;
  const systemDark = window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? true;
  return systemDark ? 'dark' : 'light';
}

export function applyDisplayPreferences(preferences: DisplayPreferences): void {
  document.documentElement.dataset.theme = resolvedTheme(preferences.theme);
  document.documentElement.dataset.reducedMotion = String(preferences.motion === 'reduced');
}

/** Flip the visible theme between dark and light. System becomes an explicit choice. */
export function toggleResolvedTheme(): DisplayPreferences {
  const current = readDisplayPreferences();
  const next: DisplayPreferences = {
    ...current,
    theme: resolvedTheme(current.theme) === 'dark' ? 'light' : 'dark',
  };
  saveDisplayPreferences(next);
  return next;
}

export function saveDisplayPreferences(preferences: DisplayPreferences): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(preferences));
  } catch {
    // The preference still applies for this page when storage is unavailable.
  }
  applyDisplayPreferences(preferences);
}

export function initialiseDisplayPreferences(): void {
  applyDisplayPreferences(readDisplayPreferences());
  window.matchMedia?.('(prefers-color-scheme: dark)').addEventListener('change', () => {
    const preferences = readDisplayPreferences();
    if (preferences.theme === 'system') applyDisplayPreferences(preferences);
  });
}
