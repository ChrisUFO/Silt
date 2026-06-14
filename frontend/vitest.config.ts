import {defineConfig} from 'vitest/config'
import {svelte} from '@sveltejs/vite-plugin-svelte'

// Vitest config for the theme engine coverage (#74). The Svelte plugin
// is required so .svelte files import cleanly from the theme store.
// jsdom provides a DOM (document.head, getElementById) for the
// injectTokens unit tests.
export default defineConfig({
  plugins: [svelte()],
  resolve: {
    preserveSymlinks: true
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
    globals: false
  }
})
