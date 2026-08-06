import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  applyDisplayPreferences,
  readDisplayPreferences,
  saveDisplayPreferences,
} from './displayPreferences';

beforeEach(() => {
  localStorage.clear();
  document.documentElement.removeAttribute('data-theme');
  document.documentElement.removeAttribute('data-reduced-motion');
  vi.stubGlobal('matchMedia', vi.fn().mockReturnValue({ matches: false, addEventListener: vi.fn() }));
});

describe('display preferences', () => {
  it('defaults to the device theme and system motion', () => {
    const preferences = readDisplayPreferences();
    applyDisplayPreferences(preferences);

    expect(preferences).toEqual({ theme: 'system', motion: 'system' });
    expect(document.documentElement.dataset.theme).toBe('light');
    expect(document.documentElement.dataset.reducedMotion).toBe('false');
  });

  it('persists explicit choices and applies them to the document', () => {
    saveDisplayPreferences({ theme: 'dark', motion: 'reduced' });

    expect(readDisplayPreferences()).toEqual({ theme: 'dark', motion: 'reduced' });
    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(document.documentElement.dataset.reducedMotion).toBe('true');
  });

  it('falls back safely when stored preferences are malformed', () => {
    localStorage.setItem('caravan.display-preferences', '{bad json');
    expect(readDisplayPreferences()).toEqual({ theme: 'system', motion: 'system' });
  });
});
