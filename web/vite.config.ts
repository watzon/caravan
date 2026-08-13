import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

// Build output is web/dist because web/embed.go embeds `all:dist` (SPEC §4).
// Do not change outDir without changing the go:embed directive.
export default defineConfig(({ mode }) => ({
  plugins: [tailwindcss(), svelte()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2022',
    assetsInlineLimit: 0, // keep woff2 as files; the Go binary embeds the tree
  },
  server: {
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
    // `caravan serve` default listen address (SPEC §10). Used when the
    // browser is pointed at Vite directly; `just dev` also reverse-proxies
    // the SPA from :8677 so either URL hot-reloads.
    proxy: { '/api': 'http://127.0.0.1:8677' },
  },
  // Under vitest the Svelte package must resolve to its browser build, which is
  // what the SPA actually ships. Leave normal builds on Vite's defaults.
  resolve: mode === 'test' ? { conditions: ['browser'] } : {},
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
    // jsdom reports this known test-environment gap on stderr; the router
    // behavior is covered by the tests and the exact line is not actionable.
    onConsoleLog(log, type) {
      if (type === 'stderr' && log.includes("Not implemented: Window's scrollTo() method")) {
        return false;
      }
      return true;
    },
  },
}));
