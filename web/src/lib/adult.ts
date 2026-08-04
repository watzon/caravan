/**
 * Pure helpers for the adult module's screens. No DOM, no I/O — unit-tested in
 * adult.test.ts.
 *
 * The visibility rules live here rather than inline in components for one
 * reason: "is any of this on screen" is the phase's whole safety property, and
 * a rule that can only be exercised by mounting four screens as six identities
 * is a rule nobody re-checks. Here it is a truth table.
 */

import type { Scene, SceneMeta, SessionUser, Site } from './api/types';
import { UNKNOWN } from './format';

/**
 * Whether the adult module exists for this browser.
 *
 * The server has already combined the two conditions — the server-wide switch
 * AND this account's grant (admins are implicitly granted) — into one boolean
 * on GET /auth/me, and this reads that and nothing else. It deliberately does
 * NOT fall back to a role check: an unknown identity reads as an admin for the
 * rest of the SPA (see session.isAdmin), and carrying that guess over here
 * would draw the nav item during boot on a server where the module is off.
 *
 * A null user is "we do not know yet", which is not "granted".
 */
export function adultVisible(user: SessionUser | null): boolean {
  return user?.adult === true;
}

/**
 * The two halves of the Adult shelf: what is on it, and what could be.
 *
 * They are separate routes rather than in-page state so each is linkable and
 * survives a reload, and the tab strip navigates between them — the same
 * arrangement the interactive release picker uses for the same reason.
 */
export type AdultTab = 'sites' | 'scenes';

export const ADULT_TABS: { key: AdultTab; label: string }[] = [
  { key: 'sites', label: 'Sites' },
  { key: 'scenes', label: 'Scenes' },
];

/** Where an Adult tab points. */
export function adultTabHref(tab: AdultTab): string {
  return tab === 'sites' ? '/adult' : '/adult/scenes';
}

/** One site's page. */
export function siteHref(site: Pick<Site, 'id'>): string {
  return `/adult/sites/${site.id}`;
}

/**
 * The scene row's leading ordinal — "#003".
 *
 * Zero-padded to three because a site's year holds hundreds of scenes and a
 * ragged left edge on a list that long is unreadable. A number past 999 keeps
 * its own width rather than being truncated.
 */
export function sceneNumber(number: number): string {
  if (!Number.isFinite(number) || number <= 0) return '#—';
  return `#${String(Math.trunc(number)).padStart(3, '0')}`;
}

/**
 * "Deep Impact · Ava Wells, Ivy Rain" — everything on a scene row after its
 * number.
 *
 * Performers are part of the line rather than a column of their own because on
 * a scene they are what a title is on an episode: the thing somebody is
 * actually looking for. An untitled scene falls back to the unknown placeholder
 * so the row never collapses to a bare number.
 */
export function sceneTitleLine(scene: Pick<Scene, 'title' | 'performers'>): string {
  const parts = [scene.title || UNKNOWN];
  const performers = (scene.performers ?? []).filter((name) => name !== '');
  if (performers.length > 0) parts.push(performers.join(', '));
  return parts.join(' · ');
}

/** The whole row as one string — "#003 Deep Impact · Ava Wells". */
export function sceneLine(scene: Pick<Scene, 'number' | 'title' | 'performers'>): string {
  return `${sceneNumber(scene.number)} ${sceneTitleLine(scene)}`;
}

/** The grid badge: "18 / 240" scenes held. */
export function sceneCountNote(site: Pick<Site, 'scene_count' | 'scene_file_count'>): string | null {
  if (site.scene_count <= 0) return null;
  return `${site.scene_file_count} / ${site.scene_count} scenes`;
}

/** Leading year of a release date ("2022-03-14" → 2022); 0 when unknown. */
export function sceneYear(date: string | null | undefined): number {
  if (!date) return 0;
  const year = Number(date.slice(0, 4));
  return Number.isInteger(year) && year > 0 ? year : 0;
}

/**
 * A scene's run time as "24 min". The provider reports seconds, and reports 0
 * far more often than it reports a real duration, so zero renders as nothing
 * rather than as "0 min".
 */
export function sceneDuration(seconds: number): string | null {
  if (!Number.isFinite(seconds) || seconds <= 0) return null;
  return `${Math.max(1, Math.round(seconds / 60))} min`;
}

/**
 * The meta line under a discover scene card: site, date, run time — whichever
 * of them the provider actually knows.
 */
export function sceneMetaLine(scene: Pick<SceneMeta, 'site_name' | 'date' | 'duration'>): string {
  const parts: string[] = [];
  if (scene.site_name) parts.push(scene.site_name);
  if (scene.date) parts.push(scene.date);
  const duration = sceneDuration(scene.duration);
  if (duration) parts.push(duration);
  return parts.join(' · ');
}
