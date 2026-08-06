/**
 * Entry point. Fonts are self-hosted woff2 (DESIGN.md §8) — latin subsets only,
 * and only the weights the type system in DESIGN.md §4 actually uses, because
 * the Go binary carries them everywhere including offline portable mode.
 */
import './fonts.css';
import './app.css';

import { mount } from 'svelte';
import App from './App.svelte';
import { initialiseDisplayPreferences } from './lib/displayPreferences';

const target = document.getElementById('app');
if (!target) throw new Error('caravan: #app mount point missing from index.html');

initialiseDisplayPreferences();

export default mount(App, { target });
