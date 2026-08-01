/** Transient action feedback (DESIGN.md §6 "Toast/banner"). */

import type { Tone } from '../status';

export interface Toast {
  id: number;
  tone: Tone;
  message: string;
}

const DISMISS_AFTER_MS = 5000;

let items = $state<Toast[]>([]);
let nextID = 1;

export const toasts = {
  get items(): Toast[] {
    return items;
  },
};

export function pushToast(message: string, tone: Tone = 'neutral'): number {
  const id = nextID++;
  items = [...items, { id, tone, message }];
  setTimeout(() => dismissToast(id), DISMISS_AFTER_MS);
  return id;
}

export function dismissToast(id: number): void {
  items = items.filter((t) => t.id !== id);
}
