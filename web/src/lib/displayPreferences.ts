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

export function applyDisplayPreferences(preferences: DisplayPreferences): void {
  const systemDark = window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? true;
  const resolvedTheme = preferences.theme === 'system'
    ? (systemDark ? 'dark' : 'light')
    : preferences.theme;

  document.documentElement.dataset.theme = resolvedTheme;
  document.documentElement.dataset.reducedMotion = String(preferences.motion === 'reduced');
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
